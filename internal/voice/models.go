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

// piperVoice is one voice of rhasspy/piper-voices, by its path there.
func piperVoice(path, name, lang, quality string, size int64, sum string, jsonSize int64, jsonSum string) Model {
	id := path[strings.LastIndex(path, "/")+1:]
	return Model{ID: id, Name: name, Lang: lang, Quality: quality, Files: []ModelFile{
		{Name: "model.onnx", URL: piperVoices + path + ".onnx", Size: size, SHA256: sum},
		{Name: "model.json", URL: piperVoices + path + ".onnx.json", Size: jsonSize, SHA256: jsonSum},
	}}
}

// PiperVoices is the catalog: Spanish and English voices in every quality, from the 14 MB x-low
// to the 114 MB high, the hashes those of the v1.0.0 tag of rhasspy/piper-voices.
var PiperVoices = []Model{
	piperVoice("es/es_MX/claude/high/es_MX-claude-high", "Claude", "es-MX", "high",
		63122309, "3ef40a71ea63852cd8ab7e6fa7d2ecdcfa67a0b47c9c48e3f10e02ee02083ea0",
		4963, "1afc81f703c0e4cb3b4d7c0dca096b8b54a98806807f0170cf5eb5557723c12d"),
	piperVoice("es/es_MX/ald/medium/es_MX-ald-medium", "Ald", "es-MX", "medium",
		63201294, "019b3803293c93e34a206dd2e53a3889209a514e786fd7144f7b70196c579b63",
		4889, "efab736e62e5321dd5d063d1b46e63c59ce655419816355b81d025c1a8d6b03c"),
	piperVoice("es/es_ES/davefx/medium/es_ES-davefx-medium", "Davefx", "es-ES", "medium",
		63201294, "6658b03b1a6c316ee4c265a9896abc1393353c2d9e1bca7d66c2c442e222a917",
		4817, "0e0dda87c732f6f38771ff274a6380d9252f327dca77aa2963d5fbdf9ec54842"),
	piperVoice("en/en_US/lessac/medium/en_US-lessac-medium", "Lessac", "en-US", "medium",
		63201294, "5efe09e69902187827af646e1a6e9d269dee769f9877d17b16b1b46eeaaf019f",
		4885, "efe19c417bed055f2d69908248c6ba650fa135bc868b0e6abb3da181dab690a0"),
	piperVoice("en/en_US/amy/medium/en_US-amy-medium", "Amy", "en-US", "medium",
		63201294, "b3a6e47b57b8c7fbe6a0ce2518161a50f59a9cdd8a50835c02cb02bdd6206c18",
		4882, "95a23eb4d42909d38df73bb9ac7f45f597dbfcde2d1bf9526fdeaf5466977d77"),
	piperVoice("en/en_GB/alba/medium/en_GB-alba-medium", "Alba", "en-GB", "medium",
		63201294, "401369c4a81d09fdd86c32c5c864440811dbdcc66466cde2d64f7133a66ad03b",
		4888, "aa965a2f02ecced632c2694e1fc72bbff6d65f265fab567ca945918c73dd89f4"),
	piperVoice("es/es_AR/daniela/high/es_AR-daniela-high", "Daniela", "es-AR", "high",
		114199011, "7ceb1fc0dab349418c5b54a639ae9ee595212d7c9ea422220d8419163d5cc985",
		7248, "aedbf69647e1d754c62ecf8e0366ca5f16af3e768e3c6b5329af6eb6bde3852b"),
	piperVoice("es/es_ES/sharvard/medium/es_ES-sharvard-medium", "Sharvard", "es-ES", "medium",
		76733615, "40febfb1679c69a4505ff311dc136e121e3419a13a290ef264fdf43ddedd0fb1",
		4903, "7438c9b699c72b0c3388dae1b68d3f364dc66a2150fe554a1c11f03372957b2c"),
	piperVoice("es/es_ES/mls_10246/low/es_ES-mls_10246-low", "MLS 10246", "es-ES", "low",
		63104526, "3f9d76d2778778942297aab052aa7b2e67248d3f43614c889ee82901a230e197",
		4160, "a865944d7f6972af347263b0b8152466dabba3b78b8fe5a6985f7d3b19fd06a1"),
	piperVoice("es/es_ES/mls_9972/low/es_ES-mls_9972-low", "MLS 9972", "es-ES", "low",
		63104526, "afdd61beb26d3aba18245c0352a8173e7f96196fbe4979e115590e8f775c95f0",
		4159, "32234a0701c480bb6ba62d4f05d0c03a95369f2133aa655370db1c00b2193340"),
	piperVoice("es/es_ES/carlfm/x_low/es_ES-carlfm-x_low", "Carlfm", "es-ES", "x-low",
		28130791, "d69677323a907cd4963f42b29c20a98b5d6bfa7f3e64df339915e4650c00d125",
		4159, "d9bdfa9ff01eb2bc9e62e7d2593939d1e4c4d8eb7cf75f972731539d12399966"),
	piperVoice("en/en_US/lessac/high/en_US-lessac-high", "Lessac", "en-US", "high",
		113895201, "4cabf7c3a638017137f34a1516522032d4fe3f38228a843cc9b764ddcbcd9e09",
		4883, "db42b97d9859f257bc1561b8ed980e7fb2398402050a74ddd6cbec931a92412f"),
	piperVoice("en/en_US/lessac/low/en_US-lessac-low", "Lessac", "en-US", "low",
		63201294, "f7d01dde371555732c4c314111ac79672b1a5ce2fc19266ab42178fd8df7f375",
		4882, "45754dfdebb3b8661c3fc564713772deec6e064feeb5b4e9594857dc7305193a"),
	piperVoice("en/en_US/amy/low/en_US-amy-low", "Amy", "en-US", "low",
		63104526, "a5a91abb7de0f104358a25aded480ddacf1ff0762886325886ec406a2e86aab3",
		4164, "2250a9a605b8dc35a116717fadc5056695dd809e34a15d02f72a0f52d53d3ebb"),
	piperVoice("en/en_US/joe/medium/en_US-joe-medium", "Joe", "en-US", "medium",
		63201294, "58afce0321b8d9c46d7cdf9c16500cc55a793b4220212dba6b70fb788b3baf06",
		4794, "3d6d5410b3795cb1950595247ef8f06190719e6fdbfa3a2356d8ec368e1aad33"),
	piperVoice("en/en_US/kristin/medium/en_US-kristin-medium", "Kristin", "en-US", "medium",
		63531379, "5849957f929cbf720c258f8458692d6103fff2f0e3d3b19c8259474bb06a18d4",
		4968, "5681426d4aead22195de70531eeeeddb46493cfaffc5764b2ea3db73428b651c"),
	piperVoice("en/en_US/hfc_female/medium/en_US-hfc_female-medium", "HFC female", "en-US", "medium",
		63201294, "914c473788fc1fa8b63ace1cdcdb44588f4ae523d3ab37df1536616835a140b7",
		5033, "03f1fa0622b80463283592d97aca9f6e89aec345a5c56b7257723e0093c58b6c"),
	piperVoice("en/en_US/hfc_male/medium/en_US-hfc_male-medium", "HFC male", "en-US", "medium",
		63201294, "d11e403a02bdf5a670c877b3dc56e0e1c8cece6fb30289586314dffdc0a78cb0",
		5033, "f66847424aed0bf99ecbb5d7cfde47c0a906f426a0daf7c46f305e7d21afd886"),
	piperVoice("en/en_US/danny/low/en_US-danny-low", "Danny", "en-US", "low",
		63104526, "56a9ae9499e961514f060aac2866cb323a21e5989592fc4f208e56fdc323ab64",
		4166, "191dea1ba9863199d8ca2e9048f83b219219fb450f62767ee02f1bc4568ce4f4"),
	piperVoice("en/en_GB/cori/high/en_GB-cori-high", "Cori", "en-GB", "high",
		114219352, "470b4dd634c98f8a4850d7626ffc3dfc90774628eeef6605a6dd8f88f30a5903",
		4963, "9e7fb5b5671612c22f3c81cbe46c1ae87b031a4632bcb509e499dad6f1e2adec"),
	piperVoice("en/en_GB/alan/medium/en_GB-alan-medium", "Alan", "en-GB", "medium",
		63201294, "0a309668932205e762801f1efc2736cd4b0120329622adf62be09e56339d3330",
		4888, "c0f0d124e5895c00e7c03b35dcc8287f319a6998a365b182deb5c8e752ee8c1e"),
	piperVoice("en/en_GB/jenny_dioco/medium/en_GB-jenny_dioco-medium", "Jenny", "en-GB", "medium",
		63201294, "469c630d209e139dd392a66bf4abde4ab86390a0269c1e47b4e5d7ce81526b01",
		4895, "a9a7a93a317c9a3cb6563e37eb057df9ef09c06188a8a4341b0fcb58cba54dd4"),
	piperVoice("en/en_GB/northern_english_male/medium/en_GB-northern_english_male-medium", "Northern English male", "en-GB", "medium",
		63201294, "57a219ae8e638873db7d18893304be5069c42868f392bb95c3ff17f0690d0689",
		4847, "69557ed3d974463453e9b0c09dd99a7ed0e52b8b87b64b357dbeeb2540a97d47"),
	piperVoice("en/en_GB/southern_english_female/low/en_GB-southern_english_female-low", "Southern English female", "en-GB", "low",
		63104526, "f2f37aed1b3a093476f719d1379ba0c0b1b1cf6f1ef99288e2ebf502971a07c3",
		4189, "a1af43310f8756161506e849a2863d342a98b0853af9e2437581d39e9922d0e5"),
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
