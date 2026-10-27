package cli

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io/fs"
    "os"
    "path/filepath"
    "regexp"
    "strings"
    "testing"

    "github.com/alecthomas/kong"
    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
)

func commandNodes(node *kong.Node) []*kong.Node {
    nodes := []*kong.Node{node}
    for _, child := range node.Children {
        if !child.Hidden {
            nodes = append(nodes, commandNodes(child)...)
        }
    }
    return nodes
}

// LOAF_UPDATE_MANUAL=1 regenerates just the command reference from the grammar.
// Ordinary tests compare without writing. Workflows and examples remain prose.
func TestCommandReferenceAndExamples(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        parser, err := newParser(&grammar{})
        if err != nil {
            t.Fatal(err)
        }
        l, _ := i18n.New(lang, nil)
        var out strings.Builder
        if lang == "en" {
            out.WriteString("# Commands and minimal calls\n\n[Home](../../README.md) · [Guide](guide.md) · [简体中文](../zh-Hans/commands.md)\n\n")
            out.WriteString("Generated from the CLI grammar. NAME, IDs, hosts and environment references in examples are placeholders. Cloud calls require a configured, applicable account and resource conditions; mutation still requires explicit authority and confirmation. See [cloud prerequisites](cloud.md). Add `--json` for noninteractive structured results, except native `ssh` and `forward`. Use an explicit run ID for agent follow-ups.\n\n")
            out.WriteString("## Global options\n\n| Option | Type | Purpose |\n| --- | --- | --- |\n")
        } else {
            out.WriteString("# 命令与最简用法\n\n[首页](../../README.zh-Hans.md) · [使用指南](guide.md) · [English](../en/commands.md)\n\n")
            out.WriteString("由 CLI 声明生成。示例中的别名、ID、主机与环境变量引用为占位值。云调用需已配置适用账号与资源条件，变更仍须明确授权和确认，详见[云资源前提](cloud.md)。添加 `--json` 获取非交互结构化结果；原生 `ssh`、`forward` 除外。Agent 后续操作应明确填写任务 ID。\n\n")
            out.WriteString("## 全局选项\n\n| 选项 | 类型 | 用途 |\n| --- | --- | --- |\n")
        }
        for _, flag := range helpData(parser.Model.Node, l).Flags {
            fmt.Fprintf(&out, "| `--%s` | `%s` | %s |\n", flag.Name, flag.Type, strings.ReplaceAll(flag.Description, "|", "\\|"))
        }
        for _, group := range []string{"experiments", "machines", "resources", "diagnostics"} {
            fmt.Fprintf(&out, "\n## %s\n\n", strings.TrimRight(l.Text("group_"+group), ":："))
            if lang == "en" {
                out.WriteString("| Command | Minimal call | Purpose |\n| --- | --- | --- |\n")
            } else {
                out.WriteString("| 命令 | 最简调用 | 用途 |\n| --- | --- | --- |\n")
            }
            for _, root := range parser.Model.Children {
                if root.Tag.Group != group {
                    continue
                }
                for _, node := range commandNodes(root) {
                    h := helpData(node, l)
                    if len(h.Examples) == 0 {
                        t.Fatalf("no minimal example: %s", node.FullPath())
                    }
                    for _, argv := range h.Examples {
                        g := &grammar{}
                        p, err := newParser(g)
                        if err != nil {
                            t.Fatal(err)
                        }
                        if _, err := parse(p, g, argv[1:], bootstrap(p.Model, argv[1:])); err != nil {
                            t.Fatalf("invalid example %v: %v", argv, err)
                        }
                    }
                    fmt.Fprintf(&out, "| `%s` | `%s` | %s |\n", node.FullPath(), strings.Join(h.Examples[0], " "), strings.ReplaceAll(l.Text(node.Help), "|", "\\|"))
                }
            }
        }
        if lang == "en" {
            out.WriteString("\n## Parameters and constraints\n\nUse `loaf help COMMAND` for parameter descriptions, built-in defaults, ranges and examples; `loaf help COMMAND --json` adds machine-readable argument and flag metadata. The same declarations produce this list and the help interface. [Argument construction](guide.md#construct-arguments-without-guessing) covers literal argv, repeated flags, CSV, false/zero/empty values and Shell boundaries. [Configuration](configuration.md) explains effective precedence. [JSON](json.md) explains result types and errors.\n\n`status` with no ID lists recent tasks; `logs`, `stop`, `outputs` and `pull` select the latest submitted or uncertain task when their ID is omitted. `wait` needs explicit IDs or tags; `clean` always needs explicit IDs. Additional common calls:\n\n")
        } else {
            out.WriteString("\n## 参数与条件\n\n使用 `loaf help COMMAND` 查看参数描述、内置默认值、范围及示例；`loaf help COMMAND --json` 增加结构化参数元数据。清单与帮助来自同一命令声明。[参数构造](guide.md#准确构造参数)说明原样 argv、重复选项、CSV、false/零/空值和 Shell 边界。[配置参考](configuration.md)解释生效优先级，[JSON](json.md)说明结果与错误。\n\n`status` 不带 ID 时列出最近任务；`logs`、`stop`、`outputs`、`pull` 不带 ID 时选择最近已提交或未确认任务。`wait` 必须明确 ID 或标签，`clean` 必须明确 ID。其他常用调用：\n\n")
        }
        out.WriteString("```bash\nloaf run\nloaf status ID\nloaf logs ID -f\nloaf wait ID1 ID2\nloaf pull ID1 ID2 --into ./comparison\nloaf check --targets a,b\nloaf cloud acquire gpu-copy --clone-from old-gpu\nloaf cloud bind gpu --request REQUEST\nloaf cloud requests REQUEST\n```\n")
        expected := out.String()
        file := filepath.Join("..", "..", "manual", lang, "commands.md")
        if os.Getenv("LOAF_UPDATE_MANUAL") == "1" {
            if err := os.WriteFile(file, []byte(expected), 0644); err != nil {
                t.Fatal(err)
            }
        }
        data, err := os.ReadFile(file)
        if err != nil || string(data) != expected {
            t.Fatalf("command reference differs: %s; regenerate with LOAF_UPDATE_MANUAL=1 go test ./internal/cli -run TestCommandReferenceAndExamples", file)
        }
    }
}

func TestPublicDocumentationLinks(t *testing.T) {
    root := filepath.Join("..", "..")
    links := regexp.MustCompile(`\[[^\]\n]+\]\(([^)]+)\)`)
    files := []string{"README.md", "README.zh-Hans.md"}
    for _, dir := range []string{"manual", "skills/loaf"} {
        err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
            if err != nil {
                return err
            }
            if !d.IsDir() && strings.HasSuffix(path, ".md") {
                rel, _ := filepath.Rel(root, path)
                files = append(files, rel)
            }
            return nil
        })
        if err != nil {
            t.Fatal(err)
        }
    }
    for _, file := range files {
        path := filepath.Join(root, file)
        data, err := os.ReadFile(path)
        if err != nil {
            t.Fatal(err)
        }
        for _, match := range links.FindAllStringSubmatch(string(data), -1) {
            link := strings.SplitN(match[1], "#", 2)[0]
            if link == "" || strings.Contains(link, "://") || strings.HasPrefix(link, "mailto:") {
                continue
            }
            if filepath.IsAbs(link) {
                t.Fatalf("nonportable public link in %s: %s", file, link)
            }
            if _, err := os.Stat(filepath.Join(filepath.Dir(path), filepath.FromSlash(link))); err != nil {
                t.Errorf("broken link in %s: %s: %v", file, link, err)
            }
        }
    }
}

func TestPublicJSONContractExamples(t *testing.T) {
    files, err := filepath.Glob(filepath.Join("..", "..", "manual", "examples", "*.json"))
    if err != nil || len(files) != 5 {
        t.Fatal(files, err)
    }
    for _, file := range files {
        data, err := os.ReadFile(file)
        if err != nil {
            t.Fatal(err)
        }
        var e struct {
            Schema int             `json:"schema_version"`
            OK     bool            `json:"ok"`
            Data   json.RawMessage `json:"data"`
            Error  *errorView      `json:"error"`
        }
        decoder := json.NewDecoder(bytes.NewReader(data))
        decoder.DisallowUnknownFields()
        if err := decoder.Decode(&e); err != nil || e.Schema != 1 {
            t.Fatal(file, err)
        }
        if strings.Contains(file, "partial-history") {
            var h execution.History
            decoder = json.NewDecoder(bytes.NewReader(e.Data))
            decoder.DisallowUnknownFields()
            if err := decoder.Decode(&h); err != nil || h.Examined != 2 || h.Unresolved != 1 || len(h.Runs) != 2 || e.OK {
                t.Fatal(file, err, h)
            }
        } else {
            var v execution.View
            decoder = json.NewDecoder(bytes.NewReader(e.Data))
            decoder.DisallowUnknownFields()
            if err := decoder.Decode(&v); err != nil || v.ID == "" || v.Version == "" || v.Submission != "submitted" {
                t.Fatal(file, err, v)
            }
            if strings.Contains(file, "training-failed") && (!e.OK || v.Status != "failed" || v.ExitCode == nil || *v.ExitCode != 7) {
                t.Fatal(file, v)
            }
            if strings.Contains(file, "state-error") && (v.Status != "succeeded" || v.StateError == "" || v.QueryError != "" || e.OK) {
                t.Fatal(file, v)
            }
            if strings.Contains(file, "unreachable") && (v.Status != "unknown" || v.ExitCode != nil || e.OK) {
                t.Fatal(file, v)
            }
        }
        if (e.Error == nil) != e.OK {
            t.Fatal("envelope contradicts outcome", file)
        }
    }
}
