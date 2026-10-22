package chaos

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// helperImage runs the fault helper containers. The tag is pinned so the
// shell utilities used by corruptScript keep their behaviour.
const helperImage = "alpine:3.20"

// corruptScript flips one byte of the file named by $1 at a random offset.
// It is passed to `sh -c` as a fixed string and the path is supplied as a
// positional argument, so the path is never parsed by a shell.
const corruptScript = `set -eu
f="$1"
[ -f "$f" ] || { echo "not a regular file: $f" >&2; exit 2; }
size=$(stat -c %s "$f")
[ "$size" -gt 0 ] || { echo "empty file: $f" >&2; exit 3; }
off=$(( $(od -An -N4 -tu4 /dev/urandom | tr -d ' ') % size ))
cur=$(od -An -tu1 -j "$off" -N1 "$f" | tr -d ' ')
new=$(( (cur + 1) % 256 ))
printf "\\$(printf '%03o' "$new")" | dd of="$f" bs=1 seek="$off" conv=notrunc 2>/dev/null
`

// pathPattern accepts a file below a node data mount such as /data2/x/y.
// Group 1 is the drive number.
var pathPattern = regexp.MustCompile(`^/data([1-9])/[A-Za-z0-9._/-]+$`)

// Faults injects drive-level faults by mounting a cluster volume in a
// short-lived helper container. Paths are given as the node sees them, for
// example /data1/unio/shard/0001, and must sit on the drive that the volume
// backs. Node-side hooks would allow stronger faults; see the README.
type Faults struct {
	runner        Runner
	volumePattern *regexp.Regexp
}

// NewFaults returns a Faults for the Compose project. Volumes must be named
// <project>_node<N>-data<M>, the names Compose gives the cluster volumes.
func NewFaults(runner Runner, project string) (*Faults, error) {
	if runner == nil {
		return nil, fmt.Errorf("chaos: nil runner")
	}
	if !projectPattern.MatchString(project) {
		return nil, fmt.Errorf("chaos: project %q must match %s", project, projectPattern)
	}
	pattern, err := regexp.Compile(`^` + regexp.QuoteMeta(project) + `_node[1-9][0-9]*-data([1-9])$`)
	if err != nil {
		return nil, fmt.Errorf("chaos: volume pattern: %w", err)
	}
	return &Faults{runner: runner, volumePattern: pattern}, nil
}

// Corrupt flips one byte of the file at path on volume. The file keeps its
// size, so only the bitrot check on read can detect the change.
func (f *Faults) Corrupt(ctx context.Context, volume, path string) error {
	mount, err := f.mount(volume, path)
	if err != nil {
		return err
	}
	return f.runHelper(ctx, []string{"-v", mount, helperImage, "sh", "-c", corruptScript, "_", path})
}

// Delete removes the file at path on volume.
func (f *Faults) Delete(ctx context.Context, volume, path string) error {
	mount, err := f.mount(volume, path)
	if err != nil {
		return err
	}
	return f.runHelper(ctx, []string{"-v", mount, helperImage, "rm", "-f", "--", path})
}

// mount validates volume and path together and returns the -v argument that
// mounts the volume at the drive path used by the node.
func (f *Faults) mount(volume, path string) (string, error) {
	vol := f.volumePattern.FindStringSubmatch(volume)
	if vol == nil {
		return "", fmt.Errorf("chaos: volume %q does not belong to this cluster", volume)
	}
	pm := pathPattern.FindStringSubmatch(path)
	if pm == nil || strings.Contains(path, "..") {
		return "", fmt.Errorf("chaos: path %q must be a file below /dataN/", path)
	}
	if pm[1] != vol[1] {
		return "", fmt.Errorf("chaos: path %q is not on volume %s", path, volume)
	}
	return volume + ":/data" + vol[1], nil
}

// runHelper starts a helper container with no network access and the given
// arguments after `docker run --rm --network none`.
func (f *Faults) runHelper(ctx context.Context, tail []string) error {
	args := append([]string{"run", "--rm", "--network", "none"}, tail...)
	if _, err := f.runner.Run(ctx, dockerBinary, args...); err != nil {
		return fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
