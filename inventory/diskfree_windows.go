package inventory

import "golang.org/x/sys/windows"

// FreeBytes is the space available to this process on dir's volume.
func FreeBytes(dir string) (int64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var avail uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, nil, nil); err != nil {
		return 0, err
	}
	return int64(avail), nil
}
