package verification

// This executor is deliberately compiled only into tests. Product builds have
// no selectable unconfined process runner, constructor, flag, or environment switch.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type testOnlyProfile struct{}

func newTestOnlyProfile() Profile  { return testOnlyProfile{} }
func (testOnlyProfile) id() string { return "test-only-unconfined" }

type capture struct {
	hash   hash.Hash
	prefix []byte
	bytes  int64
}

func newCapture() *capture { return &capture{hash: sha256.New(), prefix: []byte{}} }
func (c *capture) Write(b []byte) (int, error) {
	c.hash.Write(b)
	c.bytes += int64(len(b))
	n := min(len(b), CaptureLimit-len(c.prefix))
	c.prefix = append(c.prefix, b[:n]...)
	return len(b), nil
}
func (c *capture) stream() Stream {
	return Stream{Prefix: c.prefix, Bytes: c.bytes, SHA256: hex.EncodeToString(c.hash.Sum(nil))}
}
func (testOnlyProfile) run(ctx context.Context, root string, c Check) outcome {
	o := outcome{environment: observe("test-only-unconfined", "test-only-unconfined", c.Env["PATH"]), stdout: emptyStream(), stderr: emptyStream()}
	dir := root
	for _, part := range strings.Split(c.CWD, "/") {
		if part == "." {
			continue
		}
		dir = filepath.Join(dir, part)
		st, err := os.Lstat(dir)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			o.failure = "check cwd unavailable or unsafe"
			return o
		}
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = dir, []string{}, time.Second
	for _, k := range envKeys(c.Env) {
		cmd.Env = append(cmd.Env, k+"="+c.Env[k])
	}
	stdout, stderr := newCapture(), newCapture()
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		o.exit = &code
	}
	if err != nil {
		o.failure = err.Error()
	}
	o.stdout, o.stderr = stdout.stream(), stderr.stream()
	return o
}
