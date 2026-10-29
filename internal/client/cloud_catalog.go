package client

import (
    "context"
    "errors"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/fault"
)

// CloudCatalog reads canonical platform choices with the selected account/product.
func (c *Resources) CloudCatalog(ctx context.Context, selection CloudSelect, in cloud.CatalogInput) (cloud.CatalogPage, error) {
    if in.Limit < 1 || in.Limit > 1000 {
        return cloud.CatalogPage{}, cloudConfigError()
    }
    name, a, err := c.selectAccount(selection)
    if err != nil {
        return cloud.CatalogPage{}, err
    }
    product := selectedProduct(selection, a, "")
    return c.readCloudCatalog(ctx, name, a, product, in)
}

// CloudCloneCatalog uses a source's frozen principal, even after its account
// alias has been removed or replaced. It does not mutate the source.
func (c *Resources) CloudCloneCatalog(ctx context.Context, selection CloudSelect, source string, in cloud.CatalogInput) (cloud.CatalogPage, error) {
    b, err := c.binding(selection, source)
    if err != nil {
        return cloud.CatalogPage{}, err
    }
    return c.readCloudCatalog(ctx, b.Ref.Account, b.Settings, b.Ref.Product, in)
}

// CloudCloneSearch compares candidates against already resolved clone constraints
// with the source's frozen credential reference, never the current account alias.
func (c *Resources) CloudCloneSearch(ctx context.Context, in CloudSearchInput, source string) (cloud.SearchResult, error) {
    if in.Limit < 1 || in.Limit > 1000 {
        return cloud.SearchResult{}, cloudConfigError()
    }
    b, err := c.binding(in.Select, source)
    if err != nil {
        return cloud.SearchResult{}, err
    }
    plan, err := c.resourcePlan(ctx, in.Plan)
    if err != nil {
        return cloud.SearchResult{}, err
    }
    plan.Product, plan.AutoDL.Compatibility = b.Ref.Product, in.Plan.Compatibility
    p, err := c.Cloud.Open(ctx, b.Ref.Account, b.Settings, b.Ref.Product)
    if err != nil {
        return cloud.SearchResult{}, cloud.Failure(err)
    }
    result, err := c.Cloud.SearchWindow(ctx, p, plan, in.Limit, in.Offset)
    return result, cloud.Failure(err)
}

func (c *Resources) readCloudCatalog(ctx context.Context, name string, a cloud.Account, product string, in cloud.CatalogInput) (cloud.CatalogPage, error) {
    if in.Limit < 1 || in.Limit > 1000 {
        return cloud.CatalogPage{}, cloudConfigError()
    }
    if a.Credential.Mode == "developer" {
        return cloud.CatalogPage{Account: name, Product: product, Category: in.Category, Items: []cloud.CatalogItem{}}, fault.New("UNSUPPORTED_CAPABILITY", "cloud_catalog_unsupported", nil)
    }
    p, err := c.Cloud.Open(ctx, name, a, product)
    if err != nil {
        return cloud.CatalogPage{}, cloud.Failure(err)
    }
    catalog, ok := p.(cloud.Catalog)
    if !ok {
        return cloud.CatalogPage{}, fault.New("UNSUPPORTED_CAPABILITY", "cloud_catalog_unsupported", nil)
    }
    result, err := catalog.Catalog(ctx, in)
    var api *cloud.APIError
    if errors.As(err, &api) && api.Code == "UNSUPPORTED_CAPABILITY" {
        return result, fault.New("UNSUPPORTED_CAPABILITY", "cloud_catalog_unsupported", nil)
    }
    result.Account, result.Product = name, product
    return result, cloud.Failure(err)
}
