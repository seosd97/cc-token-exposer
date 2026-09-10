//go:build !unix

package cache

import "os"

func ownedByCaller(os.FileInfo) bool { return true }
