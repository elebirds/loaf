"""Use downloaded model parameters for inference without installing PyTorch."""

import argparse
import json
import math
from pathlib import Path


def verify(directory):
    output = Path(directory) / "outputs"
    model = json.loads((output / "model.json").read_text(encoding="utf-8"))
    cases = json.loads((output / "validation.json").read_text(encoding="utf-8"))
    metrics = json.loads((output / "metrics.json").read_text(encoding="utf-8"))
    if not cases["inputs"] or not (len(cases["inputs"]) == len(cases["labels"]) == len(cases["logits"])):
        raise ValueError("validation cases are empty or incomplete")
    maximum_error, correct = 0.0, 0
    for features, label, expected in zip(cases["inputs"], cases["labels"], cases["logits"]):
        values = features
        for layer in model["layers"]:
            if len(layer["weight"]) != len(layer["bias"]) or any(len(row) != len(values) for row in layer["weight"]):
                raise ValueError("model parameter dimensions do not match")
            values = [sum(weight * value for weight, value in zip(row, values)) + bias for row, bias in zip(layer["weight"], layer["bias"])]
            if layer["activation"] == "relu":
                values = [max(0.0, value) for value in values]
            elif layer["activation"] != "linear":
                raise ValueError("unknown activation")
        if len(values) != len(expected) or not all(math.isfinite(value) for value in values):
            raise ValueError("invalid inference result")
        maximum_error = max(maximum_error, *(abs(actual - wanted) for actual, wanted in zip(values, expected)))
        correct += max(range(len(values)), key=values.__getitem__) == label
    accuracy = correct / len(cases["inputs"])
    if maximum_error > 1e-4 or abs(accuracy - metrics["validation_accuracy"]) > 1e-6:
        raise ValueError(f"downloaded model differs from training: error={maximum_error}, accuracy={accuracy}")
    if not metrics["checkpoint_reloaded"] or (output / "checkpoint.pt").stat().st_size == 0:
        raise ValueError("checkpoint is missing or was not reloaded")
    if metrics["final_loss"] >= metrics["initial_loss"] or accuracy < 0.95:
        raise ValueError("training did not reach the example's acceptance threshold")
    return {"validation_accuracy": accuracy, "maximum_logit_error": maximum_error, "cases": len(cases["inputs"]), "device": metrics["device"]}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", help="downloaded run directory containing outputs/")
    print(json.dumps(verify(parser.parse_args().directory), indent=2))
