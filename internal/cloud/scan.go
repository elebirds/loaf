package cloud

import "context"

// inventoryScan belongs to one foreground observation. It never stores mutable
// paging state on a shared provider or treats several rounds as one snapshot.
type inventoryScan struct {
    provider Inventory
    offset   int
}

func (s *inventoryScan) read(ctx context.Context, plan Plan, limit int) (InventoryPage, error) {
    if pages, ok := s.provider.(PagedInventory); ok {
        return pages.SearchWindow(ctx, plan, limit, s.offset)
    }
    return s.provider.Search(ctx, plan, limit)
}

func (s *inventoryScan) advance(page InventoryPage) {
    if page.HasMore && page.NextOffset > s.offset {
        s.offset = page.NextOffset
    } else {
        s.offset = 0
    }
}

// SearchWindow is an optional read-only browse operation; existing Search keeps
// its original bounded prefix semantics for every provider.
func (s *Service) SearchWindow(ctx context.Context, p Provider, plan Plan, limit, offset int) (SearchResult, error) {
    inventory, ok := p.(Inventory)
    if !ok {
        return SearchResult{Plan: plan}, &APIError{Code: "UNSUPPORTED_CAPABILITY"}
    }
    if offset < 0 {
        return SearchResult{Plan: plan}, invalid()
    }
    if offset > 0 {
        if _, ok := inventory.(PagedInventory); !ok {
            return SearchResult{Plan: plan}, &APIError{Code: "UNSUPPORTED_CAPABILITY"}
        }
    }
    scan := inventoryScan{provider: inventory, offset: offset}
    page, err := scan.read(ctx, plan, limit)
    return Select(page, plan), err
}
