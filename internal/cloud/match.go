package cloud

import (
    "fmt"
    "slices"
    "sort"
    "strconv"
    "strings"
)

// Money stores an exact decimal amount and currency, never binary floating point.
type Money struct {
    Amount   string `json:"amount"`
    Currency string `json:"currency"`
}

// ParseMoney accepts nonnegative decimal amounts with at most six fractional digits.
func ParseMoney(value, currency string) (Money, error) {
    if currency != "CNY" || value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
        return Money{}, invalid()
    }
    parts := strings.Split(value, ".")
    if len(parts) > 2 || len(parts[0]) == 0 || len(parts[0]) > 9 {
        return Money{}, invalid()
    }
    if len(parts) == 2 && (len(parts[1]) == 0 || len(parts[1]) > 6) {
        return Money{}, invalid()
    }
    for _, c := range value {
        if c != '.' && (c < '0' || c > '9') {
            return Money{}, invalid()
        }
    }
    return Money{Amount: value, Currency: currency}, nil
}

func micros(m Money) int64 {
    parts := strings.SplitN(m.Amount, ".", 2)
    whole, _ := strconv.ParseInt(parts[0], 10, 64)
    fraction := ""
    if len(parts) == 2 {
        fraction = parts[1]
    }
    fraction += strings.Repeat("0", 6-len(fraction))
    sub, _ := strconv.ParseInt(fraction, 10, 64)
    return whole*1000000 + sub
}

// Less compares validated same-currency amounts exactly.
func Less(a, b Money) bool { return micros(a) < micros(b) }

// MilliCNY normalizes a platform integer price with a verified milli-CNY unit.
func MilliCNY(raw int64, multiplier int) (*Money, error) {
    if raw < 0 || multiplier < 1 || multiplier > 64 || raw > 999999999000/int64(multiplier) {
        return nil, invalid()
    }
    value := raw * int64(multiplier)
    m, err := ParseMoney(fmt.Sprintf("%d.%03d", value/1000, value%1000), "CNY")
    return &m, err
}

// Match returns stable reasons, including unknown fields required by a filter.
func Match(o Offer, r Requirements) []string {
    reasons := []string{}
    if o.Available == nil {
        reasons = append(reasons, "availability_unknown")
    } else if !*o.Available {
        reasons = append(reasons, "no_stock")
    }
    if r.GPUVendor != "" && !strings.EqualFold(o.Vendor, r.GPUVendor) {
        reasons = append(reasons, "gpu_vendor")
    }
    if len(r.GPUModels) > 0 && !slices.Contains(r.GPUModels, o.Model) {
        reasons = append(reasons, "gpu_model")
    }
    if o.GPUCount != r.GPUCount {
        reasons = append(reasons, "gpu_count")
    }
    for _, field := range []struct {
        name   string
        min    int
        actual *int
    }{
        {"gpu_memory", r.MinGPUMemoryGiB, o.GPUMemoryGiB}, {"cpus", r.MinCPUs, o.CPUs}, {"memory", r.MinMemoryGiB, o.MemoryGiB}, {"disk", r.MinDiskGiB, o.DiskGiB},
    } {
        if field.min > 0 && (field.actual == nil || *field.actual < field.min) {
            reason := field.name
            if field.actual == nil {
                reason += "_unknown"
            }
            reasons = append(reasons, reason)
        }
    }
    if len(r.Regions) > 0 && !slices.Contains(r.Regions, o.Region) {
        reasons = append(reasons, "region")
    }
    if o.ImageAvailable != nil && !*o.ImageAvailable {
        reasons = append(reasons, "image_unavailable")
    }
    if r.MaxComputeHourlyPrice != nil {
        if o.ComputeHourlyPrice == nil {
            reasons = append(reasons, "price_unknown")
        } else if o.ComputeHourlyPrice.Currency != r.MaxComputeHourlyPrice.Currency || Less(*r.MaxComputeHourlyPrice, *o.ComputeHourlyPrice) {
            reasons = append(reasons, "price")
        }
    }
    return reasons
}

// SearchResult keeps rejection and pagination coverage beside selected offers.
type SearchResult struct {
    Plan     Plan                `json:"plan"`
    Matches  []Offer             `json:"matches"`
    Rejected map[string][]string `json:"rejected"`
    InventoryPage
}

func Select(page InventoryPage, plan Plan) SearchResult {
    r := SearchResult{Plan: plan, Matches: []Offer{}, Rejected: map[string][]string{}, InventoryPage: page}
    // Offers are already represented by matches/rejections; avoid duplicate JSON.
    r.Offers = nil
    for _, o := range page.Offers {
        if reasons := Match(o, plan.Requirements); len(reasons) > 0 {
            r.Rejected[o.ID] = reasons
        } else {
            r.Matches = append(r.Matches, o)
        }
    }
    sort.SliceStable(r.Matches, func(i, j int) bool {
        a, b := r.Matches[i], r.Matches[j]
        if a.ComputeHourlyPrice != nil && b.ComputeHourlyPrice == nil {
            return true
        }
        if a.ComputeHourlyPrice == nil && b.ComputeHourlyPrice != nil {
            return false
        }
        if a.ComputeHourlyPrice != nil && b.ComputeHourlyPrice != nil {
            if Less(*a.ComputeHourlyPrice, *b.ComputeHourlyPrice) {
                return true
            }
            if Less(*b.ComputeHourlyPrice, *a.ComputeHourlyPrice) {
                return false
            }
        }
        return a.ID < b.ID
    })
    return r
}
