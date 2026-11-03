package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeJudge answers the classification and the pairs from fixed text, and records what it was asked.
type fakeJudge struct {
	classify, pairs string
	err             error
	asked           []string
	cancel          func()
}

func (f *fakeJudge) judge(_ context.Context, system, user string) (string, error) {
	f.asked = append(f.asked, user)
	if f.cancel != nil {
		f.cancel()
	}
	if f.err != nil {
		return "", f.err
	}
	if system == classifySystem {
		return f.classify, nil
	}
	return f.pairs, nil
}

func fixedNow(t *testing.T) {
	t.Helper()
	old := now
	now = func() time.Time { return time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = old })
}

func TestBackupAndRestore(t *testing.T) {
	fixedNow(t)
	v := Open(t.TempDir())
	root := t.TempDir()
	write(t, v, "a.md", "A")
	write(t, v, "dir/b.md", "B")
	name, err := v.Backup(root)
	if err != nil || name != "20261103-180000" {
		t.Fatalf("backup = %q %v", name, err)
	}
	second, _ := v.Backup(root)
	if second != "20261103-180000-2" {
		t.Errorf("a second copy in the same second gets its own name: %q", second)
	}
	if got := Backups(root); len(got) != 2 || got[0] != second {
		t.Errorf("backups = %v", got)
	}
	write(t, v, "a.md", "changed")
	write(t, v, "new.md", "new")
	_ = v.Delete("dir/b.md")
	if err := v.Restore(root, name); err != nil {
		t.Fatal(err)
	}
	a, _, _ := v.Read("a.md")
	b, _, _ := v.Read("dir/b.md")
	if a != "A" || b != "B" || len(v.List()) != 2 {
		t.Errorf("restored a=%q b=%q list=%v", a, b, v.List())
	}
	for _, bad := range []string{"../x", "20261103-999999", "nope"} {
		if err := v.Restore(root, bad); !errors.Is(err, ErrBadBackup) {
			t.Errorf("Restore(%q) = %v", bad, err)
		}
	}
	if Backups(filepath.Join(root, "missing")) != nil {
		t.Error("no folder, no copies")
	}
}

func TestBackupAndRestoreFailures(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "a.md", "A")
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, []byte("x"), 0o600)
	if _, err := v.Backup(file); err == nil {
		t.Error("a copy under a file")
	}
	if writeCopy(file, "x/a.md", "x") == nil || writeCopy(t.TempDir(), ".", "x") == nil ||
		writeCopy(t.TempDir(), "../out.md", "x") != ErrBadPath {
		t.Error("writeCopy must fail on a file parent, on its own folder and on a route out")
	}
	root := t.TempDir()
	name, _ := v.Backup(root)
	big := filepath.Join(root, name, "big.md")
	_ = os.WriteFile(big, []byte(strings.Repeat("x", MaxEntryBytes+1)), 0o600)
	if err := v.Restore(root, name); err == nil {
		t.Error("an entry the memory cannot take")
	}
	old := readFile
	defer func() { readFile = old }()
	readFile = func(string) ([]byte, error) { return nil, errors.New("disk") }
	if _, err := v.Backup(t.TempDir()); err == nil {
		t.Error("an entry that cannot be read")
	}
	if err := v.Restore(root, name); err == nil {
		t.Error("a copy that cannot be read")
	}
}

func TestReorganizeWithoutAModel(t *testing.T) {
	fixedNow(t)
	v := Open(t.TempDir())
	write(t, v, "Projects/shop.md", "---\nauthor: motita\n---\n\n- make test runs the gate\n- pnpm, not npm\n")
	write(t, v, "shop/Lessons.md", "---\ntype: lesson\n---\n\n- pnpm, not npm\n")
	write(t, v, "Projects/api.md", "- go 1.26 is needed\n")
	write(t, v, "Projects/deep/x.md", "left alone")
	write(t, v, "index.md", "see [[Projects/api]] and [[api]]")
	write(t, v, "deploy.md", "# Deploy\n\nThe staging server deploys every friday with the release script and a tag.")
	write(t, v, "release.md", "# Release\n\nThe release script deploys the staging server every friday with a tag.")
	var steps []string
	rep, err := v.Reorganize(context.Background(), t.TempDir(), nil, func(p Progress) { steps = append(steps, p.Step) })
	if err != nil {
		t.Fatal(err)
	}
	if rep.Joined != 2 || rep.UsedModel || rep.Backup == "" || rep.Linked != 1 || rep.Entries != 6 {
		t.Errorf("report = %+v", rep)
	}
	shop, _, _ := v.Read("shop/Lessons.md")
	if strings.Count(shop, "pnpm, not npm") != 1 || !strings.Contains(shop, "make test runs the gate") {
		t.Errorf("shop = %q", shop)
	}
	api, _, _ := v.Read("api/Lessons.md")
	if !strings.Contains(api, "type: lesson") {
		t.Errorf("api = %q", api)
	}
	index, _, _ := v.Read("index.md")
	if index != "see [[api/Lessons]] and [[api/Lessons]]" {
		t.Errorf("index = %q", index)
	}
	deploy, _, _ := v.Read("deploy.md")
	if !strings.Contains(deploy, `related: "[[release]]"`) {
		t.Errorf("deploy = %q", deploy)
	}
	if strings.Join(steps, ",") != "backup,join,associate" {
		t.Errorf("steps = %v", steps)
	}
	// A second run finds the pair already tied.
	again, _ := v.Reorganize(context.Background(), t.TempDir(), nil, nil)
	if again.Linked != 0 || again.Joined != 0 {
		t.Errorf("again = %+v", again)
	}
}

// memoryForTheModel is a small memory with a duplicate, a newer plan, a disagreement and a
// relation for the model to judge.
func memoryForTheModel(t *testing.T) *Store {
	t.Helper()
	v := Open(t.TempDir())
	write(t, v, "a.md", "# A\n\nThe staging server deploys every friday with the release script and a tag.")
	write(t, v, "b.md", "---\nrelated: \"[[c]]\"\n---\nThe staging server deploys every friday with the release script and a tag. Also [[a]].")
	write(t, v, "c.md", "---\ntype: fact\n---\nThe release script tags the staging server build every friday morning.")
	write(t, v, "note 1.md", "The release script tags the staging server build every friday night now.")
	return v
}

func TestReorganizeWithAModel(t *testing.T) {
	fixedNow(t)
	v := memoryForTheModel(t)
	j := &fakeJudge{
		classify: "Here:\n```json\n" + `[{"n":0,"type":"fact","title":""},{"n":1,"type":"Decisión","title":"Staging deploys"},` +
			`{"n":2,"type":"nonsense","title":"Friday tags"},{"n":9,"type":"fact"}]` + "\n```",
		pairs: `[{"n":0,"relation":"duplicate","keep":"a"},{"n":1,"relation":"replaces","keep":"b"},` +
			`{"n":2,"relation":"contradicts"},{"n":3,"relation":"related"},{"n":4,"relation":"none"},{"n":-1}]`,
	}
	rep, err := v.Reorganize(context.Background(), t.TempDir(), j.judge, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.UsedModel || rep.Typed != 2 || rep.Renamed != 2 || rep.Unanswered != 0 {
		t.Errorf("report = %+v", rep)
	}
	if rep.Merged+rep.Replaced+rep.Contradictions+rep.Linked == 0 {
		t.Errorf("no pair was judged: %+v\n%v", rep, j.asked)
	}
	for _, e := range v.List() {
		body, _, _ := v.Read(e.Path)
		if strings.Contains(body, "[[note 1]]") || strings.Contains(body, "[[b]]") {
			t.Errorf("%s still links to a moved entry: %q", e.Path, body)
		}
	}
	if !strings.Contains(strings.Join(j.asked, "\n"), `<entry n=0 title="a">`) {
		t.Errorf("asked = %v", j.asked)
	}
}

func TestApplyPair(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "a.md", "---\nrelated: \"[[x]], [[b]]\"\n---\nA says this\nshared line")
	write(t, v, "b.md", "---\nreplaces: \"[[y]]\"\n---\nB says that\nshared line")
	write(t, v, "x.md", "x")
	write(t, v, "y.md", "y")
	a, b := Entry{Path: "a.md", Title: "a"}, Entry{Path: "b.md", Title: "b"}
	var rep ReorgReport
	gone := map[string]bool{}
	v.applyPair(pair{a, b}, judged{Relation: "replaces", Keep: "b"}, &rep, gone)
	v.applyPair(pair{a, b}, judged{Relation: "contradicts"}, &rep, gone)
	v.applyPair(pair{a, b}, judged{Relation: "duplicate", Keep: "B"}, &rep, gone)
	if rep.Replaced != 1 || rep.Contradictions != 1 || rep.Merged != 1 || !gone["a.md"] {
		t.Fatalf("report = %+v gone %v", rep, gone)
	}
	body, _, _ := v.Read("b.md")
	for _, want := range []string{"A says this", "B says that", "[[x]]", "[[y]]"} {
		if !strings.Contains(body, want) {
			t.Errorf("merged lacks %q: %q", want, body)
		}
	}
	if strings.Count(body, "shared line") != 1 {
		t.Errorf("a line both say is kept once: %q", body)
	}
	// A duplicate whose entry cannot be merged is not counted.
	v.applyPair(pair{Entry{Path: "gone.md"}, b}, judged{Relation: "duplicate", Keep: "b"}, &rep, gone)
	v.applyPair(pair{Entry{Path: "gone.md"}, b}, judged{Relation: "related"}, &rep, gone)
	if rep.Merged != 1 || rep.Linked != 0 {
		t.Errorf("report = %+v", rep)
	}
}

func TestMergeIntoFailures(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "a.md", strings.Repeat("a line\n", 1)+strings.Repeat("x", MaxEntryBytes/2))
	write(t, v, "b.md", strings.Repeat("y", MaxEntryBytes/2+10))
	if v.mergeInto("missing.md", "b.md") == nil {
		t.Error("no source")
	}
	if v.mergeInto("a.md", "../b.md") == nil {
		t.Error("a bad destination")
	}
	if v.mergeInto("a.md", "b.md") == nil {
		t.Error("a merge too big for one entry")
	}
	write(t, v, "c.md", "same")
	write(t, v, "d.md", "same")
	if err := v.mergeInto("c.md", "d.md"); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := v.Read("d.md"); d != "same" {
		t.Errorf("nothing new, nothing added: %q", d)
	}
	if v.update("missing.md", func(b string) string { return b }) == nil {
		t.Error("update of nothing")
	}
}

func TestJoinProjectsSkipsWhatCannotMove(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "Projects/shop.md", "- a lesson")
	write(t, v, "shop/Lessons.md", "known")
	old := readFile
	defer func() { readFile = old }()
	readFile = func(p string) ([]byte, error) {
		if strings.HasSuffix(p, "Lessons.md") {
			return nil, errors.New("disk")
		}
		return old(p)
	}
	if n := v.joinProjects(); n != 0 {
		t.Errorf("joined %d", n)
	}
}

func TestReorganizeStopsAndSurvivesAModelThatFails(t *testing.T) {
	v := memoryForTheModel(t)
	if _, err := v.Reorganize(context.Background(), filepath.Join(t.TempDir(), "f", "\x00"), nil, nil); err == nil {
		t.Error("a copy that cannot be taken stops it")
	}
	failing := &fakeJudge{err: errors.New("offline")}
	rep, err := v.Reorganize(context.Background(), t.TempDir(), failing.judge, nil)
	if err != nil || rep.Unanswered == 0 || rep.Typed != 0 {
		t.Errorf("rep = %+v err %v", rep, err)
	}
	garbled := &fakeJudge{classify: "no idea", pairs: "[not json"}
	rep, _ = v.Reorganize(context.Background(), t.TempDir(), garbled.judge, nil)
	if rep.Unanswered < 2 {
		t.Errorf("rep = %+v", rep)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := &fakeJudge{err: errors.New("cancelled"), cancel: cancel}
	if _, err := v.Reorganize(ctx, t.TempDir(), stop.judge, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled while classifying: %v", err)
	}
	done, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := v.Reorganize(done, t.TempDir(), (&fakeJudge{}).judge, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled before: %v", err)
	}
	if _, err := v.Reorganize(done, t.TempDir(), nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled before pairing: %v", err)
	}
}

func TestPairsAreBoundedAndJudgedOnce(t *testing.T) {
	v := Open(t.TempDir())
	for i := range maxPairs + 10 {
		write(t, v, "n"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+string(rune('a'+i/26))+".md",
			"The staging server deploys every friday with the release script and a tag.")
	}
	if got := len(v.pairs()); got != maxPairs {
		t.Errorf("pairs = %d", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &fakeJudge{pairs: "[]", err: errors.New("stop"), cancel: cancel}
	var rep ReorgReport
	if err := v.associateAll(ctx, j.judge, &rep, func(Progress) {}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	if _, err := v.judgePairs(context.Background(), (&fakeJudge{pairs: "[]"}).judge, nil); err != nil {
		t.Error(err)
	}
	if readJSON("] no [", &rep) == nil {
		t.Error("no array")
	}
}

func TestApplyClassKeepsATitleThatCannotBeUsed(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "dir/a.md", "x")
	write(t, v, "dir/Taken.md", "y")
	var rep ReorgReport
	for _, title := range []string{strings.Repeat("long ", 20), "a", "Taken", "###", "New / name"} {
		v.applyClass(Entry{Path: "dir/a.md", Title: "a"}, classified{Title: title}, &rep)
	}
	if rep.Renamed != 1 || !exists(filepath.Join(v.Root(), "dir", "New name.md")) {
		t.Errorf("rep = %+v list %v", rep, v.List())
	}
}
