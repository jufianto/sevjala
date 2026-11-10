// Command brandcheck fails when a colour in the code drifts from brand/tokens.json or
// has no token there. Run it from anywhere in the repository: go run ./scripts/brandcheck.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	root, files, err := repoFiles()
	if err != nil {
		fmt.Fprintln(os.Stderr, "brandcheck:", err)
		os.Exit(2)
	}
	data, err := os.ReadFile(filepath.Join(root, "brand", "tokens.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "brandcheck:", err)
		os.Exit(2)
	}
	p, probs := loadPalette(data)
	found, uses := check(os.DirFS(root), files, p)
	probs = append(probs, found...)
	for _, pr := range probs {
		fmt.Println(pr)
	}
	if len(probs) > 0 {
		fmt.Printf("brandcheck: %d problem(s); see docs/reference/brand.md\n", len(probs))
		os.Exit(1)
	}
	fmt.Printf("brandcheck: ok, %d colours in the code match %d tokens\n", uses, len(p.Colors))
}

// repoFiles lists tracked files plus new ones git does not ignore.
func repoFiles() (string, []string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", nil, fmt.Errorf("not in a git checkout: %w", err)
	}
	root := strings.TrimSpace(string(out))
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	out, err = cmd.Output()
	if err != nil {
		return "", nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for f := range bytes.SplitSeq(bytes.TrimRight(out, "\x00"), []byte{0}) {
		name := string(f)
		if fi, err := os.Stat(filepath.Join(root, name)); err == nil && fi.Mode().IsRegular() {
			files = append(files, name)
		}
	}
	return root, files, nil
}
