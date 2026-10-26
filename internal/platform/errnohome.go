package platform

import "errors"

// ErrNoHome reports that the per-user base directory could not be
// resolved (no HOME / %AppData% / %LocalAppData% in the environment);
// callers fall back to a temp dir.
var ErrNoHome = errors.New("user home/base directory is not available")
