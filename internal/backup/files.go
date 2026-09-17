package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Instance-local write injection exercises real partial files and ENOSPC without
// root-only mounts. Production uses os.File.Write. There are no global hooks.
type fileOps struct {
	write func(*os.File, []byte) (int, error)
}

func (o fileOps) put(f *os.File, b []byte) error {
	write := o.write
	if write == nil {
		write = func(f *os.File, b []byte) (int, error) { return f.Write(b) }
	}
	n, err := write(f, b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return err
}
func digest(v string) bool {
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == 32 && strings.ToLower(v) == v
}
func overlaps(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	return a == b || strings.HasPrefix(a, b+string(os.PathSeparator)) || strings.HasPrefix(b, a+string(os.PathSeparator))
}

func cleanPath(dir string) (string, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" || strings.ContainsRune(dir, 0) {
		return "", errors.New("invalid_destination")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(dir)), nil
}
func sourceRoot(dir string) (*os.Root, error) {
	clean, err := cleanPath(dir)
	if err != nil {
		return nil, err
	}
	st, err := os.Lstat(clean)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("unsafe_directory")
	}
	return os.OpenRoot(clean)
}

// emptyTarget checks emptiness while holding the SAME directory-inode flock
// used by store.Open. The returned release closes both handles. This excludes a
// racing daemon or second restore before any final state.db can be published.
func emptyTarget(dir string) (_ *os.Root, release func(), err error) {
	clean, err := cleanPath(dir)
	if err != nil {
		return nil, nil, err
	}
	if err = os.Mkdir(clean, 0700); err != nil && !os.IsExist(err) {
		return nil, nil, err
	}
	root, err := sourceRoot(clean)
	if err != nil {
		return nil, nil, err
	}
	var unlock func()
	defer func() {
		if err != nil {
			root.Close()
			if unlock != nil {
				unlock()
			}
		}
	}()
	if unlock, err = lockTarget(root); err != nil {
		return nil, nil, err
	}
	st, err := root.Stat(".")
	if err != nil {
		return nil, nil, err
	}
	if st.Mode().Perm() != 0700 {
		return nil, nil, errors.New("target_not_private")
	}
	f, err := root.Open(".")
	if err != nil {
		return nil, nil, err
	}
	names, e := f.Readdirnames(1)
	closeErr := f.Close()
	if e != nil && e != io.EOF {
		return nil, nil, e
	}
	if closeErr != nil {
		return nil, nil, closeErr
	}
	if len(names) > 0 {
		return nil, nil, errors.New("target_not_empty")
	}
	parent, err := os.Open(filepath.Dir(clean))
	if err == nil {
		err = errors.Join(parent.Sync(), parent.Close())
	}
	if err != nil {
		return nil, nil, err
	}
	return root, func() { root.Close(); unlock() }, nil
}

func regular(root *os.Root, name string) (*os.File, error) {
	if !fs.ValidPath(name) {
		return nil, errors.New("unsafe_path")
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		st, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, err
		}
		if st.Mode()&os.ModeSymlink != 0 || i < len(parts)-1 && !st.IsDir() || i == len(parts)-1 && !st.Mode().IsRegular() {
			return nil, errors.New("unsafe_file")
		}
	}
	st, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		f.Close()
		return nil, errors.New("source_changed")
	}
	return f, nil
}
func hashFile(ctx context.Context, root *os.Root, name string) (Blob, error) {
	f, err := regular(root, name)
	if err != nil {
		return Blob{}, err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			return Blob{}, err
		}
		n, e := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			total += int64(n)
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return Blob{}, e
		}
	}
	return Blob{hex.EncodeToString(h.Sum(nil)), total}, nil
}
func checkFile(ctx context.Context, r *os.Root, name string, want Blob) error {
	got, err := hashFile(ctx, r, name)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("digest_mismatch")
	}
	return nil
}
func readBounded(root *os.Root, name string, max int) ([]byte, error) {
	f, err := regular(root, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > max {
		return nil, errors.New("oversized")
	}
	return b, nil
}
func (o fileOps) copy(ctx context.Context, src *os.Root, source string, dst *os.Root, destination string, want Blob) (err error) {
	in, err := regular(src, source)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if st.Size() != want.Bytes {
		return errors.New("digest_mismatch")
	}
	if err = dst.MkdirAll(path.Dir(destination), 0700); err != nil {
		return err
	}
	out, err := dst.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, out.Close())
		if err != nil {
			_ = dst.Remove(destination)
		}
	}()
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var total int64
	limited := io.LimitReader(in, want.Bytes+1)
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, e := limited.Read(buf)
		if n > 0 {
			if err = o.put(out, buf[:n]); err != nil {
				return err
			}
			h.Write(buf[:n])
			total += int64(n)
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	if total != want.Bytes || hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
		return errors.New("digest_mismatch")
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return syncPath(dst, path.Dir(destination))
}
func (o fileOps) writeFile(ctx context.Context, r *os.Root, name string, b []byte) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	f, err := r.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if err = o.put(f, b); err != nil {
		return err
	}
	return f.Sync()
}
func syncPath(r *os.Root, name string) error {
	f, err := r.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
func syncRoot(r *os.Root) error { return syncPath(r, ".") }
func syncTree(r *os.Root) error {
	return fs.WalkDir(r.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("unsafe_file")
		}
		if d.IsDir() {
			return syncPath(r, name)
		}
		return nil
	})
}
func exactTree(r *os.Root, allowed map[string]bool) error {
	return fs.WalkDir(r.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("unsafe_file")
		}
		if !d.IsDir() && !allowed[name] {
			return errors.New("unlisted_file")
		}
		return nil
	})
}
