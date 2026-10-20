"""Train a small CUDA classifier without downloading data or dependencies."""

import argparse
import json
from pathlib import Path
import time

import torch
from torch import nn


def dataset(count, generator):
    labels = torch.randint(0, 2, (count,), generator=generator)
    angle = torch.rand(count, generator=generator) * (2 * torch.pi)
    radius = 0.5 + labels.float() * 0.7
    points = torch.stack((angle.cos(), angle.sin()), dim=1) * radius[:, None]
    points += torch.randn(count, 2, generator=generator) * 0.06
    return points, labels


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--device", choices=("cpu", "cuda"), default="cuda")
    parser.add_argument("--steps", type=int, default=400)
    parser.add_argument("--max-seconds", type=float, default=120)
    args = parser.parse_args()
    if args.steps < 1 or args.max_seconds <= 0:
        parser.error("steps and max-seconds must be positive")
    if args.device == "cuda" and not torch.cuda.is_available():
        parser.error("CUDA is unavailable; select an existing CUDA environment or explicitly use --device cpu")

    torch.manual_seed(7)
    torch.set_num_threads(1)
    generator = torch.Generator().manual_seed(13)
    train_x, train_y = dataset(4096, generator)
    valid_x, valid_y = dataset(1024, generator)
    device = torch.device(args.device)
    train_x, train_y = train_x.to(device), train_y.to(device)
    valid_device = valid_x.to(device)
    model = nn.Sequential(nn.Linear(2, 32), nn.ReLU(), nn.Linear(32, 32), nn.ReLU(), nn.Linear(32, 2)).to(device)
    optimizer = torch.optim.Adam(model.parameters(), lr=0.003)
    criterion = nn.CrossEntropyLoss()
    with torch.no_grad():
        initial_loss = criterion(model(train_x), train_y).item()
    device_name = torch.cuda.get_device_name(device) if device.type == "cuda" else "CPU"
    print(json.dumps({"phase": "training", "device": str(device), "device_name": device_name, "initial_loss": initial_loss}), flush=True)

    begin = time.monotonic()
    history = []
    for step in range(1, args.steps + 1):
        indices = torch.randint(len(train_x), (256,), device=device)
        optimizer.zero_grad(set_to_none=True)
        loss = criterion(model(train_x[indices]), train_y[indices])
        loss.backward()
        optimizer.step()
        if step == 1 or step % 100 == 0:
            row = {"step": step, "loss": loss.item(), "seconds": time.monotonic() - begin}
            history.append(row)
            print(json.dumps(row), flush=True)
        if time.monotonic() - begin >= args.max_seconds:
            break

    with torch.no_grad():
        final_loss = criterion(model(train_x), train_y).item()
        logits = model(valid_device).cpu()
        accuracy = (logits.argmax(dim=1) == valid_y).float().mean().item()
    model = model.cpu()
    output = Path("outputs")
    output.mkdir(exist_ok=True)
    torch.save(model.state_dict(), output / "checkpoint.pt")
    restored = nn.Sequential(nn.Linear(2, 32), nn.ReLU(), nn.Linear(32, 32), nn.ReLU(), nn.Linear(32, 2))
    restored.load_state_dict(torch.load(output / "checkpoint.pt", map_location="cpu", weights_only=True))
    with torch.no_grad():
        restored_logits = restored(valid_x)
        torch.testing.assert_close(restored_logits, logits, rtol=1e-4, atol=1e-4)

    layers = []
    for index in (0, 2, 4):
        layer = model[index]
        layers.append({"weight": layer.weight.detach().tolist(), "bias": layer.bias.detach().tolist(), "activation": "relu" if index < 4 else "linear"})
    metrics = {
        "device": str(device), "device_name": device_name, "torch_version": torch.__version__,
        "steps": step, "seconds": time.monotonic() - begin,
        "initial_loss": initial_loss, "final_loss": final_loss, "validation_accuracy": accuracy,
        "peak_cuda_mib": torch.cuda.max_memory_allocated(device) / (1024 * 1024) if device.type == "cuda" else 0,
        "checkpoint_reloaded": True,
    }
    documents = {
        "metrics.json": metrics, "history.json": history,
        "model.json": {"layers": layers},
        "validation.json": {"inputs": valid_x.tolist(), "labels": valid_y.tolist(), "logits": restored_logits.tolist()},
    }
    for name, value in documents.items():
        (output / name).write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"phase": "completed", **metrics}), flush=True)


if __name__ == "__main__":
    main()
