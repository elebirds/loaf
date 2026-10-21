package cloud

import (
    "bytes"
    "context"
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
    "regexp"
    "sort"
    "time"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/fault"
    "golang.org/x/sys/unix"
)

// Receipt is a minimal write-ahead intent and current result. It contains no
// token/password, raw API responses, training state machine, or event history.
type Receipt struct {
    FormatVersion int       `json:"format_version"`
    ID            string    `json:"request_id"`
    Operation     string    `json:"operation"`
    Account       string    `json:"account"`
    Settings      Account   `json:"account_settings"`
    Target        string    `json:"target,omitempty"`
    RemoteBase    string    `json:"remote_base,omitempty"`
    Plan          Plan      `json:"plan"`
    AllowUnquoted bool      `json:"allow_unquoted"`
    CreatedAt     time.Time `json:"created_at"`
    Deadline      time.Time `json:"deadline"`
    Attempts      int       `json:"attempts"`
    Status        string    `json:"status"`
    Instance      *Ref      `json:"instance,omitempty"`
    Offer         *Offer    `json:"offer,omitempty"`
}

// Receipts is concrete private local JSON storage, not a database abstraction.
type Receipts struct{ Dir string }

var receiptID = regexp.MustCompile(`^c_[0-9a-f]{24}$`)
var localName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var lockScope = regexp.MustCompile(`^[A-Za-z0-9_-]{1,240}$`)

func (s Receipts) path(id string) (string, error) {
    if !receiptID.MatchString(id) {
        return "", invalid()
    }
    return filepath.Join(s.Dir, id+".json"), nil
}

func (r Receipt) validate() error {
    if r.FormatVersion != 1 || !receiptID.MatchString(r.ID) || !localName.MatchString(r.Account) || r.Settings.Validate() != nil || r.CreatedAt.IsZero() || r.Deadline.Before(r.CreatedAt) || r.Attempts < 0 {
        return invalid()
    }
    if r.Operation != "acquire" && r.Operation != "start" && r.Operation != "stop" {
        return invalid()
    }
    switch r.Status {
    case "intent", "uncertain", "created", "ready", "accepted", "completed", "rejected", "no_stock", "not_created":
    default:
        return invalid()
    }
    product := r.Plan.Product
    if r.Operation != "acquire" {
        if r.Instance == nil {
            return invalid()
        }
        product = r.Instance.Product
    }
    if !ValidProduct(product) || r.Instance != nil && (r.Instance.ID == "" || r.Instance.Account != r.Account || r.Instance.Product != product || r.Instance.Provider != r.Settings.Provider) {
        return invalid()
    }
    return nil
}

// New allocates an ID before the first potentially billable API call.
func (s Receipts) New(r Receipt) (Receipt, error) {
    var id [12]byte
    if _, err := rand.Read(id[:]); err != nil {
        return r, err
    }
    r.ID, r.FormatVersion = "c_"+hex.EncodeToString(id[:]), 1
    return r, nil
}

// Save uses durable, private atomic replacement. Callers serialize the relevant
// allocation, instance power operation, or receipt update.
func (s Receipts) Save(r Receipt) error {
    if err := r.validate(); err != nil {
        return err
    }
    name, err := s.path(r.ID)
    if err != nil {
        return err
    }
    data, err := json.MarshalIndent(r, "", "    ")
    if err != nil {
        return err
    }
    return atomicfile.Write(name, append(data, '\n'))
}

// Load rejects unknown fields/formats without changing the original receipt.
func (s Receipts) Load(id string) (Receipt, error) {
    var r Receipt
    name, err := s.path(id)
    if err != nil {
        return r, err
    }
    data, err := os.ReadFile(name)
    if err != nil {
        return r, fault.Wrap("BAD_CONFIG", "cloud_receipt", err)
    }
    decoder := json.NewDecoder(bytes.NewReader(data))
    decoder.DisallowUnknownFields()
    if decoder.Decode(&r) != nil || decoder.Decode(new(any)) != io.EOF || r.ID != id || r.validate() != nil {
        return r, fault.New("BAD_CONFIG", "cloud_receipt", nil)
    }
    return r, nil
}

// List reads a bounded local inventory; malformed receipts must not be skipped
// when deciding whether another allocation could duplicate an uncertain one.
func (s Receipts) List() ([]Receipt, error) {
    rows := []Receipt{}
    entries, err := os.ReadDir(s.Dir)
    if errors.Is(err, os.ErrNotExist) {
        return rows, nil
    }
    if err != nil {
        return nil, err
    }
    if len(entries) > 10000 {
        return nil, fault.New("BAD_CONFIG", "cloud_receipt", nil)
    }
    for _, entry := range entries {
        if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
            continue
        }
        r, err := s.Load(entry.Name()[:len(entry.Name())-5])
        if err != nil {
            return rows, err
        }
        rows = append(rows, r)
    }
    sort.Slice(rows, func(i, j int) bool {
        if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
            return rows[i].ID < rows[j].ID
        }
        return rows[i].CreatedAt.After(rows[j].CreatedAt)
    })
    return rows, nil
}

// Lock refuses concurrent mutating operations rather than queueing a stale
// approval. Locks live only for this foreground invocation.
func (s Receipts) Lock(ctx context.Context, scope string, fn func() error) error {
    if !lockScope.MatchString(scope) {
        return invalid()
    }
    if err := ctx.Err(); err != nil {
        return err
    }
    if err := os.MkdirAll(s.Dir, 0700); err != nil {
        return err
    }
    file, err := os.OpenFile(filepath.Join(s.Dir, scope+".lock"), os.O_CREATE|os.O_RDWR, 0600)
    if err != nil {
        return err
    }
    defer file.Close()
    if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
        return fault.New("CLOUD_BUSY", "cloud_busy", nil)
    }
    defer unix.Flock(int(file.Fd()), unix.LOCK_UN)
    return fn()
}

func (s Receipts) pending(account, instance string) error {
    rows, err := s.List()
    if err != nil {
        return err
    }
    for _, r := range rows {
        relevant := r.Operation == "acquire" && instance == "" || instance != "" && r.Operation != "acquire" && r.Instance != nil && r.Instance.ID == instance
        if relevant && r.Account == account && (r.Status == "intent" || r.Status == "uncertain") {
            f := fault.New("CLOUD_UNCONFIRMED", "cloud_pending", map[string]any{"ID": r.ID})
            f.Hint = "cloud_reconcile_hint"
            return f
        }
    }
    return nil
}
