package releases

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestListRemoteTagsAgainstALocalRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo,
			"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	git("commit", "-q", "--allow-empty", "-m", "init")
	for _, tag := range []string{"2.135.0", "2.135.10", "2.135.2", "2.1350.0", "2.13.5", "2.136.0", "v2.135.9", "2.135.3-rc1", "2.135.04"} {
		git("tag", tag)
	}
	git("tag", "-a", "-m", "annotated", "2.135.11")

	got, err := ListRemoteTags(context.Background(), repo, "2.135")
	if err != nil {
		t.Fatal(err)
	}
	if want := "[2.135.0 2.135.2 2.135.10 2.135.11]"; fmt.Sprint(got) != want {
		t.Fatalf("tags = %v, want %s", got, want)
	}

	if got, err := ListRemoteTags(context.Background(), repo, "9.9"); err != nil || len(got) != 0 {
		t.Fatalf("empty minor: %v, %v", got, err)
	}
	if _, err := ListRemoteTags(context.Background(), filepath.Join(t.TempDir(), "missing"), "2.135"); err == nil {
		t.Fatal("no error for a missing repository")
	}
	pwned := filepath.Join(t.TempDir(), "pwned")
	if _, err := ListRemoteTags(context.Background(), "--upload-pack=touch "+pwned, "2.135"); err == nil {
		t.Fatal("an option-like repository was accepted")
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("an option-like repository ran its upload-pack")
	}
	if _, err := ListRemoteTags(context.Background(), repo, "2.135.*"); err == nil {
		t.Fatal("an invalid minor was accepted")
	}
}
