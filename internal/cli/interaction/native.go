package interaction

import "context"

// WithNative closes an active renderer, joins its input worker, and gives one
// native operation exclusive stdin ownership. It does not reopen a renderer or
// replay the operation. The callback must use native streams, not this session's
// prompting methods, because those require the same input owner.
func (s *InputSession) WithNative(ctx context.Context, progress *LiveProgress, operation func() error) error {
    if progress != nil {
        if err := progress.Close(); err != nil {
            return err
        }
    }
    if err := s.acquire(ctx, false); err != nil {
        return err
    }
    defer s.release()
    return operation()
}
