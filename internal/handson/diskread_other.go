//go:build !linux

package handson

import "errors"

// ReadDisk: raw reads are Linux only.
func (s *System) ReadDisk(name string, _ bool) DiskJob {
	return doneJob{DiskRead{Disk: name, Skipped: "raw reads need Linux"}}
}

// BootDriveSpeed: raw reads are Linux only.
func BootDriveSpeed(disk, _ string) BootRead {
	return BootRead{Disk: disk, Err: "raw reads need Linux"}
}

// CheckTestFile: raw reads are Linux only.
func CheckTestFile(name string) (string, error) { return "", errors.New("raw reads need Linux") }
