// Package i18n localizes only the client-owned presentation layer.
package i18n

import (
    "embed"
    "io/fs"
    "strings"

    "github.com/elebirds/loaf/internal/fault"
    goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
    "github.com/pelletier/go-toml/v2"
    "golang.org/x/text/language"
)

//go:embed locales/*.toml
var catalogs embed.FS

var bundle = loadBundle()

func loadBundle() *goi18n.Bundle {
    b := goi18n.NewBundle(language.English)
    b.RegisterUnmarshalFunc("toml", toml.Unmarshal)
    files, err := fs.Glob(catalogs, "locales/*.toml")
    if err != nil {
        panic(err)
    }
    for _, name := range files {
        data, err := catalogs.ReadFile(name)
        if err != nil {
            panic(err)
        }
        if _, err := b.ParseMessageFileBytes(data, name); err != nil {
            panic(err)
        }
    }
    return b
}

// Supported lists languages loaded from the embedded catalogs.
func Supported() []string {
    var result []string
    for _, t := range bundle.LanguageTags() {
        result = append(result, t.String())
    }
    return result
}

// Localizer renders complete messages with named parameters.
type Localizer struct {
    Locale    string
    localizer *goi18n.Localizer
}

// New resolves a requested language or the first nonempty POSIX locale.
func New(preferred string, env map[string]string) (*Localizer, error) {
    auto := preferred == "" || preferred == "auto"
    if auto {
        for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
            if env[key] != "" {
                preferred = env[key]
                break
            }
        }
    }
    locale, ok := match(preferred)
    if !ok && !auto {
        return English(), fault.New("BAD_CONFIG", "language_invalid", map[string]any{"Languages": strings.Join(Supported(), ", ")})
    }
    if !ok {
        locale = "en"
    }
    return &Localizer{locale, goi18n.NewLocalizer(bundle, locale)}, nil
}

func match(raw string) (string, bool) {
    raw = strings.SplitN(raw, ".", 2)[0]
    if raw == "C" || raw == "POSIX" {
        return "en", true
    }
    raw = strings.ReplaceAll(raw, "_", "-")
    tag, err := language.Parse(raw)
    if err != nil {
        return "", false
    }
    base, _ := tag.Base()
    script, _ := tag.Script()
    for _, supported := range bundle.LanguageTags() {
        b, _ := supported.Base()
        s, _ := supported.Script()
        if b == base && s == script {
            return supported.String(), true
        }
    }
    return "", false
}

// English is the error-rendering fallback when an explicit language is invalid.
func English() *Localizer { return &Localizer{"en", goi18n.NewLocalizer(bundle, "en")} }

// Text renders a known catalog entry; a missing entry is a programming error.
func (l *Localizer) Text(id string, data ...map[string]any) string {
    c := &goi18n.LocalizeConfig{MessageID: id}
    if len(data) != 0 {
        c.TemplateData = data[0]
    }
    return l.localizer.MustLocalize(c)
}
