package cloud

import (
    "bytes"
    "context"
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "errors"
    "io"
    "path/filepath"
    "regexp"
    "sort"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/filelock"
    "github.com/elebirds/loaf/internal/privatefs"
    "github.com/elebirds/loaf/internal/state/receipts"
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

// Receipts validates cloud intents backed by the shared local SQLite database.
type Receipts struct{ Dir string }

var receiptID = regexp.MustCompile(`^c_[0-9a-f]{24}$`)
var localName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var lockScope = regexp.MustCompile(`^[A-Za-z0-9_-]{1,240}$`)

func (s Receipts) store() receipts.Store {
    return receipts.Store{Path: filepath.Join(s.Dir, "state.db")}
}

func (r Receipt) validate() error {
    if r.FormatVersion != 1 || !receiptID.MatchString(r.ID) || !localName.MatchString(r.Account) || r.Settings.Validate() != nil || r.CreatedAt.IsZero() || (!r.Plan.Continuous && r.Deadline.Before(r.CreatedAt)) || (r.Plan.Continuous && (r.Operation != "acquire" || !r.Deadline.IsZero())) || r.Attempts < 0 {
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
    if r.Plan.Clone != nil && (r.Operation != "acquire" || validateClone(r.Plan, r.Account) != nil || r.Instance != nil && *r.Instance == r.Plan.Clone.Instance) {
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

// Save durably records an intent or outcome before releasing its operation lock.
func (s Receipts) Save(r Receipt) error {
    if err := r.validate(); err != nil {
        return err
    }
    data, err := json.Marshal(r)
    if err != nil {
        return err
    }
    return s.store().SaveEvent(context.Background(), r.ID, r.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"), data, "cloud:"+r.ID+":"+r.Status, r.publicEvent())
}

func (r Receipt) publicEvent() *event.Envelope {
    kind := ""
    switch {
    case r.Status == "uncertain":
        kind = "resource.attention_required"
    case r.Operation == "acquire" && r.Status == "created":
        kind = "resource.acquired"
    case r.Operation == "acquire" && r.Status == "ready":
        kind = "resource.ready"
    case r.Operation == "start" && r.Status == "completed":
        kind = "resource.started"
    case r.Operation == "stop" && r.Status == "completed":
        kind = "resource.stopped"
    }
    if kind == "" {
        return nil
    }
    e := &event.Envelope{Type: kind, Subject: event.Subject{Kind: "resource_operation", ID: r.ID}, Correlation: event.Correlation{CloudRequestID: r.ID}, Data: event.Data{Status: r.Status}, OccurredAt: time.Now().UTC()}
    if r.Status == "uncertain" {
        e.Data.Code = "CLOUD_UNCONFIRMED"
    }
    if r.Instance != nil {
        e.Subject = event.Subject{Kind: "resource", ID: r.Instance.Provider + ":" + r.Instance.Product + ":" + r.Instance.ID}
        e.Data.InstanceID = r.Instance.ID
        e.Data.Product = r.Instance.Product
    }
    return e
}

func decodeReceipt(data []byte) (Receipt, error) {
    var r Receipt
    decoder := json.NewDecoder(bytes.NewReader(data))
    decoder.DisallowUnknownFields()
    if decoder.Decode(&r) != nil || decoder.Decode(new(any)) != io.EOF || r.validate() != nil {
        return r, fault.New("BAD_CONFIG", "cloud_receipt", nil)
    }
    return r, nil
}

// Load rejects unknown receipt formats without modifying state.
func (s Receipts) Load(id string) (Receipt, error) {
    if !receiptID.MatchString(id) {
        return Receipt{}, invalid()
    }
    data, err := s.store().Load(context.Background(), id)
    if err != nil {
        return Receipt{}, fault.Wrap("BAD_CONFIG", "cloud_receipt", err)
    }
    r, err := decodeReceipt(data)
    if err == nil && r.ID != id {
        err = fault.New("BAD_CONFIG", "cloud_receipt", nil)
    }
    return r, err
}

// List retains malformed-row failures: an unreadable intent cannot authorize
// another paid operation that might duplicate it.
func (s Receipts) List() ([]Receipt, error) {
    data, err := s.store().List(context.Background())
    if err != nil {
        return nil, err
    }
    if len(data) > 10000 {
        return nil, fault.New("BAD_CONFIG", "cloud_receipt", nil)
    }
    rows := make([]Receipt, 0, len(data))
    for _, raw := range data {
        r, err := decodeReceipt(raw)
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
    if err := privatefs.MkdirAll(s.Dir); err != nil {
        return err
    }
    file, err := filelock.Acquire(ctx, filepath.Join(s.Dir, scope+".lock"), 0)
    if err != nil {
        if errors.Is(err, filelock.ErrBusy) {
            return fault.New("CLOUD_BUSY", "cloud_busy", nil)
        }
        return err
    }
    defer file.Close()
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
