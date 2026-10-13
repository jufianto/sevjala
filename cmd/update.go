package cmd

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	releasesAPI  = "https://api.github.com/repos/jufianto/sevjala/releases?per_page=30"
	releasesPage = "https://github.com/jufianto/sevjala/releases"
	offlineHint  = "Without Internet: download the file for this PC from " + releasesPage +
		" on another PC, copy it here and run: sevjala update --from FILE"
)

var errUpToDate = errors.New("already up to date")

func newUpdateCmd(g *globals) *cobra.Command {
	var from string
	c := &cobra.Command{
		Use:   "update",
		Short: "Update Sevjala on this PC to the newest release",
		Long: `Downloads the newest Sevjala release for this PC from GitHub, checks it against the
release's checksums.txt and that it runs here, then stops the service, replaces the
installed sevjala and starts it again. Settings, pairings and backups are kept. If the new
version does not start, the previous one is put back.

This is the only command that uses the Internet; the service never does. Without
Internet, download the file on another PC and use --from. Update every PC: the
controller and each backup PC.`,
		Example: `  sevjala update
  sevjala update --from D:\sevjala-win7-amd64.exe     (a file you downloaded yourself)`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := requireAdmin(); err != nil {
				return err
			}
			if from != "" {
				abs, err := filepath.Abs(from)
				if err != nil {
					return fail(err)
				}
				from = abs
			}
			flow, err := newSetupFlow(g, c, from)
			switch {
			case err != nil:
				return fail(err)
			case flow.role == "":
				return failf("Sevjala is not installed on this PC; run 'sevjala setup' from the new file instead")
			}
			out := c.OutOrStdout()
			want := ""
			if from == "" {
				ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
				defer stop()
				fmt.Fprintf(out, "this PC runs %s; checking %s\n", version, releasesPage)
				file, tag, err := fetchUpdate(ctx, &http.Client{Timeout: 10 * time.Minute}, releasesAPI, version, runtime.GOOS, runtime.GOARCH, flow.dir)
				if errors.Is(err, errUpToDate) {
					fmt.Fprintf(out, "already up to date (the newest release is %s)\n", tag)
					return nil
				}
				if err != nil {
					return err
				}
				defer os.Remove(file)
				fmt.Fprintf(out, "downloaded %s, checksum ok\n", tag)
				flow.self, want = file, strings.TrimPrefix(tag, "v")
			} else if sameFile(from, flow.target) {
				return failf("%s is the installed file; give the new one", from)
			}
			if err := runsHere(flow.self, want); err != nil {
				return err
			}
			if err := flow.update(); err != nil {
				return fail(err)
			}
			return nil
		},
	}
	c.Flags().StringVar(&from, "from", "", "install this sevjala `FILE` instead of downloading one")
	return c
}

// runsHere checks that file is a Sevjala that starts on this PC, and that it
// is version want when want is not empty.
func runsHere(file, want string) error {
	raw, err := exec.Command(file, "version").Output()
	f := strings.Fields(string(raw))
	switch {
	case err != nil || len(f) < 2 || f[0] != "sevjala":
		return failf("%s is not a Sevjala that runs on this PC; nothing was changed", file)
	case want != "" && f[1] != want:
		return failf("%s says it is %s, not %s; nothing was changed", file, f[1], want)
	}
	return nil
}

type release struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r release) asset(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

// assetName is the release file for a PC; the win7 build runs on Windows 7 to 11.
func assetName(goos, goarch string) string {
	if goos == "windows" {
		return "sevjala-win7-" + goarch + ".exe"
	}
	return "sevjala-" + goos + "-" + goarch
}

// latestRelease is the newest release for a PC running current: pre-releases
// only when it runs one already (or a build without a version).
func latestRelease(rs []release, current string) (release, bool) {
	cur, ok := parseVersion(current)
	pre := !ok || len(cur.pre) > 0
	var best release
	var bestV version3
	found := false
	for _, r := range rs {
		v, ok := parseVersion(r.Tag)
		if !ok || r.Draft || (!pre && (r.Prerelease || len(v.pre) > 0)) {
			continue
		}
		if !found || compareVersions(v, bestV) > 0 {
			best, bestV, found = r, v, true
		}
	}
	return best, found
}

// fetchUpdate downloads the newest release for goos/goarch into dir when it is
// newer than current, and returns the file and its tag.
func fetchUpdate(ctx context.Context, hc *http.Client, api, current, goos, goarch, dir string) (string, string, error) {
	var listing bytes.Buffer
	if err := get(ctx, hc, api, &listing, 4<<20); err != nil {
		return "", "", err
	}
	var rs []release
	if err := json.Unmarshal(listing.Bytes(), &rs); err != nil {
		return "", "", failf("GitHub's list of releases could not be read; try again later. %s", offlineHint)
	}
	r, ok := latestRelease(rs, current)
	if !ok {
		return "", "", failf("no release found at %s", releasesPage)
	}
	if cur, ok := parseVersion(current); ok {
		if v, _ := parseVersion(r.Tag); compareVersions(v, cur) <= 0 {
			return "", r.Tag, errUpToDate
		}
	}
	name := assetName(goos, goarch)
	asset, sums := r.asset(name), r.asset("checksums.txt")
	if asset == "" || sums == "" {
		return "", r.Tag, failf("%s has no build for this PC (%s/%s); see %s", r.Tag, goos, goarch, releasesPage)
	}
	var list bytes.Buffer
	if err := get(ctx, hc, sums, &list, 64<<10); err != nil {
		return "", r.Tag, err
	}
	want := ""
	for _, line := range strings.Split(list.String(), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	if want == "" {
		return "", r.Tag, failf("the checksums of %s do not list %s; nothing was changed", r.Tag, name)
	}
	tmp, err := os.CreateTemp(dir, "sevjala-update-*"+filepath.Ext(name))
	if err != nil {
		return "", r.Tag, failf("cannot write in %s: %w", dir, err)
	}
	h := sha256.New()
	err = get(ctx, hc, asset, io.MultiWriter(tmp, h), 1<<30)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = failf("the download of %s did not match its checksum; nothing was changed. Try again", name)
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o755)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", r.Tag, err
	}
	return tmp.Name(), r.Tag, nil
}

func get(ctx context.Context, hc *http.Client, url string, w io.Writer, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "sevjala/"+version)
	resp, err := hc.Do(req)
	if err != nil {
		return netProblem(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return failf("GitHub answered %q; try again later (it limits how often one PC may ask). %s", resp.Status, offlineHint)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	switch {
	case err != nil:
		return netProblem(err)
	case n > limit:
		return failf("GitHub sent more than expected; nothing was changed")
	}
	return nil
}

func netProblem(err error) error {
	var cv *tls.CertificateVerificationError
	var ua x509.UnknownAuthorityError
	switch {
	case errors.Is(err, context.Canceled):
		return failf("stopped; nothing was changed")
	case errors.As(err, &cv), errors.As(err, &ua):
		return failf("this PC does not trust GitHub's certificate (an old Windows 7 needs its root certificate updates). %s", offlineHint)
	}
	return failf("cannot reach GitHub (is this PC on the Internet?). %s", offlineHint)
}

// version3 is a semantic version without build metadata.
type version3 struct {
	nums [3]int
	pre  []string
}

func parseVersion(s string) (version3, bool) {
	s, _, _ = strings.Cut(strings.TrimPrefix(s, "v"), "+")
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	var v version3
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v.nums[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
	}
	return v, true
}

// compareVersions follows semver precedence: a release is newer than its
// pre-releases; numeric identifiers sort below text ones.
func compareVersions(a, b version3) int {
	for i := range a.nums {
		if c := cmp.Compare(a.nums[i], b.nums[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) == 0 || len(b.pre) == 0:
		return cmp.Compare(len(b.pre), len(a.pre))
	}
	for i := range min(len(a.pre), len(b.pre)) {
		x, xerr := strconv.Atoi(a.pre[i])
		y, yerr := strconv.Atoi(b.pre[i])
		var c int
		switch {
		case xerr == nil && yerr == nil:
			c = cmp.Compare(x, y)
		case xerr == nil:
			c = -1
		case yerr == nil:
			c = 1
		default:
			c = strings.Compare(a.pre[i], b.pre[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.pre), len(b.pre))
}
