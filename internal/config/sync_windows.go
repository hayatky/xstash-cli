//go:build windows

package config

// Windows does not expose Unix directory fsync through os.File.Sync. File data
// is flushed before replacement, but power-loss durability is not guaranteed.
func syncDirectory(string) error { return nil }
