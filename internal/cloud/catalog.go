package cloud

import (
    "context"
    "time"
)

// CatalogInput requests one bounded, read-only directory, not stock or a quote.
type CatalogInput struct {
    Category, Kind, Query string
    Vendor, Architecture  string
    Limit                 int
}

// CatalogItem carries a canonical value and actual environment metadata.
type CatalogItem struct {
    ID         string `json:"id"`
    Name       string `json:"name"`
    DataCenter string `json:"data_center,omitempty"`
    // Public reports public visibility/usage for regions; nil means unknown.
    // It is UI default metadata, not stock or proof of account entitlement.
    Public           *bool  `json:"public,omitempty"`
    Kind             string `json:"kind,omitempty"`
    Vendor           string `json:"gpu_vendor,omitempty"`
    Architecture     string `json:"architecture,omitempty"`
    Framework        string `json:"framework,omitempty"`
    FrameworkVersion string `json:"framework_version,omitempty"`
    Python           string `json:"python,omitempty"`
    CUDA             string `json:"cuda,omitempty"`
}

// CatalogPage reports bounded coverage independently of inventory availability.
type CatalogPage struct {
    Account    string        `json:"account"`
    Product    string        `json:"product"`
    Category   string        `json:"category"`
    Source     string        `json:"source,omitempty"`
    Items      []CatalogItem `json:"items"`
    Examined   int           `json:"examined"`
    Complete   bool          `json:"complete"`
    ObservedAt time.Time     `json:"observed_at"`
}

// Catalog is optional; missing directories never turn into invented choices.
type Catalog interface {
    Catalog(context.Context, CatalogInput) (CatalogPage, error)
}

// PagedInventory optionally supports a bounded window inside the same frozen
// requirements. A window after offset zero is never a complete stock snapshot.
type PagedInventory interface {
    SearchWindow(context.Context, Plan, int, int) (InventoryPage, error)
}
