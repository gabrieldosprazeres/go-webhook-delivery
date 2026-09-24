package cryptobox

import (
	"errors"
	"io"
	"os"
	"syscall"
)

func secureRead(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("cryptobox: unsafe secret file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return nil, errors.New("cryptobox: unsafe secret owner")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cryptobox: cannot open secret file")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("cryptobox: secret file changed")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(raw) == 0 || len(raw) > 64<<10 {
		clear(raw)
		return nil, errors.New("cryptobox: invalid secret size")
	}
	return raw, nil
}
