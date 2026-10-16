//go:build windows

package tools

import "golang.org/x/sys/windows"

func preferredWindowsCodePages() []uint32 {
	pages := make([]uint32, 0, 2)
	seen := make(map[uint32]struct{}, 2)
	appendUnique := func(cp uint32) {
		if cp == 0 {
			return
		}
		if _, ok := seen[cp]; ok {
			return
		}
		seen[cp] = struct{}{}
		pages = append(pages, cp)
	}

	if cp, err := windows.GetConsoleOutputCP(); err == nil {
		appendUnique(cp)
	}
	appendUnique(windows.GetACP())

	return pages
}
