package voice

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// LOCAL VOICES: Piper models the browser runs itself.
//
// The page cannot download a model from anywhere it likes: the gateway fetches each file once,
// from an address in this fixed catalog, checks its size and SHA-256, and serves it from its own
// origin. A file that does not match is never kept. Nothing here depends on the page: the list
// of addresses is compiled in.

// ModelFile is one file a local voice needs.
type ModelFile struct {
	// Name is the file's name on disk and in the URL the page fetches it from.
	Name string `json:"name"`
	// URL is where it is downloaded from. Member, when set, is its path inside that address's
	// .tgz (an npm package): the archive is read as it arrives and only the member is kept.
	URL    string `json:"-"`
	Member string `json:"-"`
	// Size and SHA256 are what the file must be, checked before it is kept.
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Model is one voice of the catalog.
type Model struct {
	ID string `json:"id"`
	// Name is the speaker's name, Lang its language as a BCP 47 tag (es-MX, en-US).
	Name    string `json:"name"`
	Lang    string `json:"lang"`
	Quality string `json:"quality"`
	// Files are the model and its settings (the .onnx.json the page reads the phonemizer
	// voice and the sample rate from).
	Files []ModelFile `json:"files"`
}

// RuntimeID is the directory, and the id in a file's URL, of the files every voice shares.
const RuntimeID = "runtime"

const (
	piperVoices = "https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/"
	// The runtime comes from the npm packages the page is built with: the versions here must
	// be the ones web/package.json pins, or the bundled code and the binary it loads disagree.
	ortPackage   = "https://registry.npmjs.org/onnxruntime-web/-/onnxruntime-web-1.30.0.tgz"
	piperPackage = "https://registry.npmjs.org/@diffusionstudio/piper-wasm/-/piper-wasm-1.0.0.tgz"
)

// PiperRuntime are the files every local voice needs: onnxruntime's WebAssembly build, which
// runs the model, and the phonemizer (espeak-ng compiled to WebAssembly, with its data).
var PiperRuntime = []ModelFile{
	{Name: "ort-wasm-simd-threaded.wasm", URL: ortPackage, Member: "package/dist/ort-wasm-simd-threaded.wasm",
		Size: 14239897, SHA256: "3398c10d07d229bd91b364548e130e0e51a8e5704b88c7c083ebbeb78842dee2"},
	{Name: "piper_phonemize.wasm", URL: piperPackage, Member: "package/build/piper_phonemize.wasm",
		Size: 635212, SHA256: "b777cd107a91d2bcc6a1ea46f2c26a662a7407394fe84589198aeaa83dd7a9d6"},
	{Name: "piper_phonemize.data", URL: piperPackage, Member: "package/build/piper_phonemize.data",
		Size: 18077249, SHA256: "29f1025eb23a5b5c192cd14a6efbce4509402ff265405072ee6f7d1a09b78f8c"},
}

// fileSum is the size and SHA-256 a downloaded file must have.
type fileSum struct {
	size int64
	sum  string
}

// piperVoice is one voice of rhasspy/piper-voices, by its path there: model is the sum of its
// model, config the sum of the JSON file that describes it.
func piperVoice(path, name, lang, quality string, model, config fileSum) Model {
	id := path[strings.LastIndex(path, "/")+1:]
	return Model{ID: id, Name: name, Lang: lang, Quality: quality, Files: []ModelFile{
		{Name: "model.onnx", URL: piperVoices + path + ".onnx", Size: model.size, SHA256: model.sum},
		{Name: "model.json", URL: piperVoices + path + ".onnx.json", Size: config.size, SHA256: config.sum},
	}}
}

// PiperVoices is the catalog: a few Spanish and English voices, the hashes those of the
// v1.0.0 tag of rhasspy/piper-voices.
var PiperVoices = []Model{
	piperVoice("es/es_MX/claude/high/es_MX-claude-high", "Claude", "es-MX", "high",
		fileSum{63122309, "3ef40a71ea63852cd8ab7e6fa7d2ecdcfa67a0b47c9c48e3f10e02ee02083ea0"},
		fileSum{4963, "1afc81f703c0e4cb3b4d7c0dca096b8b54a98806807f0170cf5eb5557723c12d"}),
	piperVoice("es/es_MX/ald/medium/es_MX-ald-medium", "Ald", "es-MX", "medium",
		fileSum{63201294, "019b3803293c93e34a206dd2e53a3889209a514e786fd7144f7b70196c579b63"},
		fileSum{4889, "efab736e62e5321dd5d063d1b46e63c59ce655419816355b81d025c1a8d6b03c"}),
	piperVoice("es/es_ES/davefx/medium/es_ES-davefx-medium", "Davefx", "es-ES", "medium",
		fileSum{63201294, "6658b03b1a6c316ee4c265a9896abc1393353c2d9e1bca7d66c2c442e222a917"},
		fileSum{4817, "0e0dda87c732f6f38771ff274a6380d9252f327dca77aa2963d5fbdf9ec54842"}),
	piperVoice("en/en_US/lessac/medium/en_US-lessac-medium", "Lessac", "en-US", "medium",
		fileSum{63201294, "5efe09e69902187827af646e1a6e9d269dee769f9877d17b16b1b46eeaaf019f"},
		fileSum{4885, "efe19c417bed055f2d69908248c6ba650fa135bc868b0e6abb3da181dab690a0"}),
	piperVoice("en/en_US/amy/medium/en_US-amy-medium", "Amy", "en-US", "medium",
		fileSum{63201294, "b3a6e47b57b8c7fbe6a0ce2518161a50f59a9cdd8a50835c02cb02bdd6206c18"},
		fileSum{4882, "95a23eb4d42909d38df73bb9ac7f45f597dbfcde2d1bf9526fdeaf5466977d77"}),
	piperVoice("en/en_GB/alba/medium/en_GB-alba-medium", "Alba", "en-GB", "medium",
		fileSum{63201294, "401369c4a81d09fdd86c32c5c864440811dbdcc66466cde2d64f7133a66ad03b"},
		fileSum{4888, "aa965a2f02ecced632c2694e1fc72bbff6d65f265fab567ca945918c73dd89f4"}),
}

// Library is the local voices on this machine: the catalog, and the directory the downloaded
// files are kept in (one subdirectory per voice, plus RuntimeID).
type Library struct {
	Dir     string
	Runtime []ModelFile
	Models  []Model
	// Client downloads the files. Nil means http.DefaultClient: a model is tens of MB, so the
	// download is bounded by its context, not by a timeout.
	Client *http.Client
}

// ErrUnknownModel is returned for an id the catalog does not have.
var ErrUnknownModel = errors.New("there is no such voice in the catalog")

// Find is the voice with this id.
func (l Library) Find(id string) (Model, bool) {
	for _, m := range l.Models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// files are the files of an id: the runtime's or a voice's.
func (l Library) files(id string) ([]ModelFile, bool) {
	if id == RuntimeID {
		return l.Runtime, true
	}
	m, ok := l.Find(id)
	return m.Files, ok
}

// present says every file is on disk with its size. The hash was checked when it was kept.
func (l Library) present(id string, files []ModelFile) bool {
	for _, f := range files {
		st, err := os.Stat(filepath.Join(l.Dir, id, f.Name))
		if err != nil || st.Size() != f.Size {
			return false
		}
	}
	return true
}

// Installed says whether a voice can be used: its files and the runtime are all on disk.
func (l Library) Installed(id string) bool {
	files, ok := l.files(id)
	return ok && l.present(id, files) && l.present(RuntimeID, l.Runtime)
}

// RuntimeInstalled says whether the shared files are on disk.
func (l Library) RuntimeInstalled() bool { return l.present(RuntimeID, l.Runtime) }

// Size is how many bytes a voice needs, its own files only.
func (m Model) Size() int64 {
	var n int64
	for _, f := range m.Files {
		n += f.Size
	}
	return n
}

// RuntimeSize is how many bytes the shared files need.
func (l Library) RuntimeSize() int64 {
	return Model{Files: l.Runtime}.Size()
}

// File is the path of one file of a voice (or of RuntimeID), when it is on disk. Only names the
// catalog lists are answered, so no name can reach outside the directory.
func (l Library) File(id, name string) (string, bool) {
	files, ok := l.files(id)
	if !ok {
		return "", false
	}
	for _, f := range files {
		if f.Name == name && l.present(id, []ModelFile{f}) {
			return filepath.Join(l.Dir, id, f.Name), true
		}
	}
	return "", false
}

// Install downloads what a voice is missing, the runtime included. progress, when not nil, is
// called with the bytes kept so far and the bytes this install needs.
func (l Library) Install(ctx context.Context, id string, progress func(done, total int64)) error {
	m, ok := l.Find(id)
	if !ok {
		return ErrUnknownModel
	}
	type need struct {
		dir string
		f   ModelFile
	}
	var needs []need
	var total int64
	for _, set := range []struct {
		dir   string
		files []ModelFile
	}{{RuntimeID, l.Runtime}, {m.ID, m.Files}} {
		for _, f := range set.files {
			if !l.present(set.dir, []ModelFile{f}) {
				needs = append(needs, need{set.dir, f})
				total += f.Size
			}
		}
	}
	var done int64
	report := func(n int64) {
		done += n
		if progress != nil {
			progress(done, total)
		}
	}
	report(0)
	// One request per address: the two phonemizer files come out of the same archive.
	for len(needs) > 0 {
		url := needs[0].f.URL
		dir := needs[0].dir
		var batch []ModelFile
		var rest []need
		for _, n := range needs {
			if n.f.URL == url {
				batch = append(batch, n.f)
				continue
			}
			rest = append(rest, n)
		}
		if err := l.fetch(ctx, url, filepath.Join(l.Dir, dir), batch, report); err != nil {
			return err
		}
		needs = rest
	}
	return nil
}

// Remove deletes a voice's files. The runtime stays: another voice may use it.
func (l Library) Remove(id string) error {
	if _, ok := l.Find(id); !ok {
		return ErrUnknownModel
	}
	return os.RemoveAll(filepath.Join(l.Dir, id))
}

// Verify hashes a voice's files on disk again, the runtime's included, and says which one is
// missing or no longer matches.
func (l Library) Verify(id string) error {
	files, ok := l.files(id)
	if !ok {
		return ErrUnknownModel
	}
	for _, set := range []struct {
		dir   string
		files []ModelFile
	}{{RuntimeID, l.Runtime}, {id, files}} {
		for _, f := range set.files {
			fh, err := os.Open(filepath.Join(l.Dir, set.dir, f.Name))
			if err != nil {
				return fmt.Errorf("%s is missing", f.Name)
			}
			h := sha256.New()
			_, err = io.Copy(h, fh)
			_ = fh.Close()
			if err != nil || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
				return fmt.Errorf("%s does not match its SHA-256", f.Name)
			}
		}
	}
	return nil
}

// fetch downloads one address and keeps the files of batch it holds.
func (l Library) fetch(ctx context.Context, url, dir string, batch []ModelFile, report func(int64)) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("the voices directory could not be created: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("the download could not be built: %w", err)
	}
	client := l.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s could not be reached: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", url, resp.StatusCode)
	}
	if batch[0].Member == "" {
		return keep(resp.Body, dir, batch[0], report)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("%s is not a .tgz: %w", url, err)
	}
	tr := tar.NewReader(gz)
	left := len(batch)
	for left > 0 {
		hdr, err := tr.Next()
		if err != nil {
			return fmt.Errorf("%s ended before %d of its files: %w", url, left, err)
		}
		for _, f := range batch {
			if f.Member == hdr.Name {
				if err := keep(tr, dir, f, report); err != nil {
					return err
				}
				left--
			}
		}
	}
	return nil
}

// keep writes one file next to its final name, and renames it into place only when its size
// and hash are the catalog's.
func keep(r io.Reader, dir string, f ModelFile, report func(int64)) error {
	tmp, err := os.CreateTemp(dir, f.Name+".*.part")
	if err != nil {
		return fmt.Errorf("%s could not be written: %w", f.Name, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h, progressWriter(report)), io.LimitReader(r, f.Size+1))
	_ = tmp.Close()
	if err != nil {
		return fmt.Errorf("%s could not be downloaded: %w", f.Name, err)
	}
	if n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return fmt.Errorf("%s is not the file the catalog names (size or SHA-256 differ): it was not kept", f.Name)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, f.Name)); err != nil {
		return fmt.Errorf("%s could not be kept: %w", f.Name, err)
	}
	return nil
}

// progressWriter counts the bytes written through it.
type progressWriter func(int64)

func (p progressWriter) Write(b []byte) (int, error) {
	p(int64(len(b)))
	return len(b), nil
}
