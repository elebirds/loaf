package cli

import "github.com/elebirds/loaf/internal/fault"

func cloudInstanceID(id string) (string, error) {
    if id == "" {
        return "", fault.Invalid("instance", "missing_argument", "cloud_instance_required")
    }
    return id, nil
}
