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
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// LOCAL VOICES: Piper voices the browser runs itself, and the Whisper
// models it listens with (speech input with no transcription service).
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
	// Engine is what runs the model in the page: EnginePiper, EngineWhisper or EngineLLM.
	Engine string `json:"engine"`
	// Files are the voice's own: a Piper model and its settings (the .onnx.json the page reads
	// the phonemizer voice and the sample rate from).
	Files []ModelFile `json:"files"`
	// Sample is a short recording of the voice, heard before it is downloaded. Nil when the
	// catalog has none.
	Sample *ModelFile `json:"-"`
	// Runtime are the files of the runtime this voice needs besides the ones every voice does
	// (Library.Runtime): Piper's phonemizer, or onnxruntime's WebGPU build for the models that listen. They are kept once,
	// in RuntimeID, for every voice that needs them.
	Runtime []ModelFile `json:"-"`
}

// The engines a local voice runs on.
const (
	EnginePiper = "piper"
	// EngineWhisper is OpenAI's Whisper, which turns speech into text: a model of the catalog that
	// listens rather than speaks.
	EngineWhisper = "whisper"
)

// RuntimeID is the directory, and the id in a file's URL, of the files every voice shares.
const RuntimeID = "runtime"

// samplesDir is the directory the samples are kept in, apart from the voices.
const samplesDir = "samples"

const (
	piperVoices = "https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/"
	// The runtime comes from the npm packages the page is built with: the versions here must
	// be the ones web/package.json pins, or the bundled code and the binary it loads disagree.
	ortPackage   = "https://registry.npmjs.org/onnxruntime-web/-/onnxruntime-web-1.30.0.tgz"
	piperPackage = "https://registry.npmjs.org/@diffusionstudio/piper-wasm/-/piper-wasm-1.0.0.tgz"
)

// OrtRuntime are the files every local voice and model needs: onnxruntime's WebAssembly build,
// which runs it on the processor.
var OrtRuntime = []ModelFile{
	{Name: "ort-wasm-simd-threaded.wasm", URL: ortPackage, Member: "package/dist/ort-wasm-simd-threaded.wasm",
		Size: 14239897, SHA256: "3398c10d07d229bd91b364548e130e0e51a8e5704b88c7c083ebbeb78842dee2"},
}

// PhonemizerRuntime is what only Piper's voices need besides OrtRuntime: the phonemizer
// (espeak-ng compiled to WebAssembly, with its data). The other engines read the text themselves.
var PhonemizerRuntime = []ModelFile{
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

// piperVoice is one voice of rhasspy/piper-voices, by its path there, with the sample the
// repository keeps next to it: model, config and sample are the sums of its model, of the JSON
// file that describes it and of the sample.
func piperVoice(path, name, lang, quality string, model, config, sample fileSum) Model {
	cut := strings.LastIndex(path, "/") + 1
	return Model{ID: path[cut:], Name: name, Lang: lang, Quality: quality, Engine: EnginePiper,
		Runtime: PhonemizerRuntime, Files: []ModelFile{
			{Name: "model.onnx", URL: piperVoices + path + ".onnx", Size: model.size, SHA256: model.sum},
			{Name: "model.json", URL: piperVoices + path + ".onnx.json", Size: config.size, SHA256: config.sum},
		},
		Sample: &ModelFile{Name: "sample.mp3", URL: piperVoices + path[:cut] + "samples/speaker_0.mp3",
			Size: sample.size, SHA256: sample.sum}}
}

// GPURuntime is onnxruntime's WebAssembly build that drives WebGPU, for the voices and models
// that run on the graphics card: one copy for all of them, in RuntimeID.
var GPURuntime = []ModelFile{{Name: "ort-wasm-simd-threaded.asyncify.wasm", URL: ortPackage,
	Member: "package/dist/ort-wasm-simd-threaded.asyncify.wasm",
	Size:   26781914, SHA256: "39f9f0894d478800487ed9f7dbe92618498db320cf55c8e3d89adff8dce658da"}}

// WhisperID starts the id of every Whisper model; WhisperGPU ends the id of those built for the
// graphics card. The page knows a model by them.
const (
	WhisperID  = "whisper"
	WhisperGPU = "-gpu"
)

// The name of a model's tokenizer, in every repository that has one.
const tokenizerJSON = "tokenizer.json"

// The parts of a Whisper export: 8-bit for the processor, 4-bit for the graphics card.
const (
	whisperEncoder8 = "encoder_model_quantized.onnx"
	whisperDecoder8 = "decoder_model_merged_quantized.onnx"
	whisperEncoder4 = "encoder_model_q4.onnx"
	whisperDecoder4 = "decoder_model_merged_q4.onnx"
)

// whisperRepo is onnx-community's ONNX export of one Whisper model, at a fixed commit.
func whisperRepo(model, commit string) string {
	return "https://huggingface.co/onnx-community/whisper-" + model + "/resolve/" + commit + "/"
}

// whisperTokenizer is the table from tokens to text, the same for every multilingual model.
func whisperTokenizer(repo string) ModelFile {
	return ModelFile{Name: tokenizerJSON, URL: repo + tokenizerJSON,
		Size: 2480466, SHA256: "27fc476bfe7f17299480be2273fc0608e4d5a99aba2ab5dec5374b4482d1a566"}
}

// whisperModel is one Whisper model: its tokenizer, what it must not write
// (generation_config.json), and its two parts, renamed encoder.onnx and decoder.onnx (the decoder
// is the one with its cache merged in). A model for the graphics card also brings GPURuntime; it
// runs on the processor too, where WebGPU fails. None needs Piper's phonemizer.
func whisperModel(id, name, quality, repo string, gen, encoder, decoder ModelFile) Model {
	gen.Name, gen.URL = "generation_config.json", repo+"generation_config.json"
	encoder.Name, encoder.URL = "encoder.onnx", repo+"onnx/"+encoder.URL
	decoder.Name, decoder.URL = "decoder.onnx", repo+"onnx/"+decoder.URL
	var runtime []ModelFile
	if strings.HasSuffix(id, WhisperGPU) {
		runtime = GPURuntime
	}
	return Model{ID: id, Name: name, Lang: "mul", Quality: quality, Engine: EngineWhisper, Runtime: runtime,
		Files: []ModelFile{whisperTokenizer(repo), gen, encoder, decoder}}
}

var (
	whisperTiny  = whisperRepo("tiny", "ff4177021cc41f7db950912b73ea4fdf7d01d8e7")
	whisperBase  = whisperRepo("base", "1846881b6b3a3024392c1eea3ad983695bc23925")
	whisperSmall = whisperRepo("small", "36050c46d777d46dc4b5f43f6d90574fc38f8732")
)

// The generation settings of each model, the same but for the alignment of its heads.
var (
	whisperTinyGen  = ModelFile{Size: 3772, SHA256: "f5c67e5a4f7102f8cb4d058bc95da276bbc19eeec997267c3bb0f25ef68facd1"}
	whisperBaseGen  = ModelFile{Size: 3832, SHA256: "61070cf8de25b1e9256e8e102ded49d8d24a8369ed36ef84fdf21549e68125a0"}
	whisperSmallGen = ModelFile{Size: 3893, SHA256: "f538b28220c6a6d6f1af1458d4141cacb4ef4963df3de98a19490440c412ddf0"}
)

// WhisperModels are the models the page listens with, in every language Whisper knows. Those for
// the processor are 8-bit, the fastest there; those for the graphics card are 4-bit, which
// WebGPU runs and 8-bit it does not.
var WhisperModels = []Model{
	whisperModel(WhisperID+"-tiny", "Whisper tiny", "low", whisperTiny, whisperTinyGen,
		ModelFile{URL: whisperEncoder8, Size: 10124990,
			SHA256: "2af4a414ca47aa30f61246017e5fe82b0a8d229281d1255ba666a2a7f6b84d19"},
		ModelFile{URL: whisperDecoder8, Size: 30719241,
			SHA256: "25e807a962b6349356d0ea5d0dfe530b7e5bf0e2a484aeca0359d03143faddd3"}),
	whisperModel(WhisperID+"-base", "Whisper base", "medium", whisperBase, whisperBaseGen,
		ModelFile{URL: whisperEncoder8, Size: 23201314,
			SHA256: "5862993336bf33acd23736071aae2b32261d3b1b2f37780194460d4ef974dd46"},
		ModelFile{URL: whisperDecoder8, Size: 53693315,
			SHA256: "fa3ef9902734ce5ae6f9ef2bdb2ba9a6c4b5785b09f4f420ce036573dc9d090b"}),
	whisperModel(WhisperID+"-small", "Whisper small", "high", whisperSmall, whisperSmallGen,
		ModelFile{URL: whisperEncoder8, Size: 92326160,
			SHA256: "a43a83f3c5361cd591cfa7c36f14b43cf7cb22f47a415cc14a8d557be800fa92"},
		ModelFile{URL: whisperDecoder8, Size: 156750845,
			SHA256: "ec07c3cbb64172c39791e26ee870a65ac22b458c36722bfe2776b3dbf741e0c9"}),
	whisperModel(WhisperID+"-base"+WhisperGPU, "Whisper base", "medium", whisperBase, whisperBaseGen,
		ModelFile{URL: whisperEncoder4, Size: 18772451,
			SHA256: "9d4e9c31e8559e71de3e0e49d60b830951484699ef53747a4a06261bbbc8cb88"},
		ModelFile{URL: whisperDecoder4, Size: 123602419,
			SHA256: "09f83b71ceedc97dab1d90b914715dc532a646a147abda11d83c64867b7c319c"}),
	whisperModel(WhisperID+"-small"+WhisperGPU, "Whisper small", "high", whisperSmall, whisperSmallGen,
		ModelFile{URL: whisperEncoder4, Size: 66182104,
			SHA256: "9f088ad0fe15ba2cf094c9fb22ac7fe1111fc9ef317c1e4d1f6a3bca38a931ba"},
		ModelFile{URL: whisperDecoder4, Size: 233149327,
			SHA256: "795c61b344576719c2b249cc9911c78b4cc552c8c6a5407be21a3380a0791b13"}),
}

// PiperVoices is the catalog of voices: only Piper's high-quality ones, in Spanish and English
// (the 114 to 121 MB models), the hashes those of the v1.0.0 tag of rhasspy/piper-voices. The
// lower tiers sound worse and were dropped.
var PiperVoices = []Model{
	piperVoice("es/es_MX/claude/high/es_MX-claude-high", "Claude", "es-MX", "high",
		fileSum{63122309, "3ef40a71ea63852cd8ab7e6fa7d2ecdcfa67a0b47c9c48e3f10e02ee02083ea0"},
		fileSum{4963, "1afc81f703c0e4cb3b4d7c0dca096b8b54a98806807f0170cf5eb5557723c12d"},
		fileSum{189537, "9022f6b03498baacffe28024506f40ac43fa5a6c533b6dd902c2f3767acbfc94"}),
	piperVoice("es/es_AR/daniela/high/es_AR-daniela-high", "Daniela", "es-AR", "high",
		fileSum{114199011, "7ceb1fc0dab349418c5b54a639ae9ee595212d7c9ea422220d8419163d5cc985"},
		fileSum{7248, "aedbf69647e1d754c62ecf8e0366ca5f16af3e768e3c6b5329af6eb6bde3852b"},
		fileSum{139548, "4d84c1235a1b61c0d764c544ad7a2ecc9e3ce3852cf31d1ba279479b0e27e959"}),
	piperVoice("en/en_US/lessac/high/en_US-lessac-high", "Lessac", "en-US", "high",
		fileSum{113895201, "4cabf7c3a638017137f34a1516522032d4fe3f38228a843cc9b764ddcbcd9e09"},
		fileSum{4883, "db42b97d9859f257bc1561b8ed980e7fb2398402050a74ddd6cbec931a92412f"},
		fileSum{78282, "eabd9e164e56e5b7d6ac36fecdb8c632e82f0fad7e73eb14a471ef95206f883d"}),
	piperVoice("en/en_GB/cori/high/en_GB-cori-high", "Cori", "en-GB", "high",
		fileSum{114219352, "470b4dd634c98f8a4850d7626ffc3dfc90774628eeef6605a6dd8f88f30a5903"},
		fileSum{4963, "9e7fb5b5671612c22f3c81cbe46c1ae87b031a4632bcb509e499dad6f1e2adec"},
		fileSum{95280, "46ce2b83a7c717cd0e0cba19e339cbbfdeee092966241ab8f885fa54c68751f2"}),
	piperVoice("en/en_US/ryan/high/en_US-ryan-high", "Ryan", "en-US", "high",
		fileSum{120786792, "b3990d7606e183ec8dbfba70a4607074f162de1a0c412e0180d1ff60bb154eca"},
		fileSum{4166, "c6d3b98f08315cb4bebf0d49d50fc4ff491b503c64b940cd3d5ca28543b48011"},
		fileSum{91225, "0b04d5c8fba5be2842709e6c6212210c6f53b7076dfbd0109e8930d8f6fd7ba4"}),
}

// Library is the local voices on this machine: the catalog, and the directory the downloaded
// files are kept in (one subdirectory per voice, plus RuntimeID).
type Library struct {
	Dir string
	// Runtime are the files every voice needs; a voice's Model.Runtime adds to them.
	Runtime []ModelFile
	Models  []Model
	// Client downloads the files. Nil means http.DefaultClient: a model is tens of MB, so the
	// download is bounded by its context, not by a timeout.
	Client *http.Client
}

// ErrUnknownModel is returned for an id the catalog does not have.
var ErrUnknownModel = errors.New("there is no such voice in the catalog")

// ErrNoSample is returned for a voice the catalog has no sample of.
var ErrNoSample = errors.New("there is no sample of this voice")

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
		return l.runtimeFiles(), true
	}
	m, ok := l.Find(id)
	return m.Files, ok
}

// fileSet is the files kept in one directory.
type fileSet struct {
	dir   string
	files []ModelFile
}

// runtimeFiles are every file kept in RuntimeID: the ones every voice needs, and those some of
// them add, each once.
func (l Library) runtimeFiles() []ModelFile {
	files := slices.Clone(l.Runtime)
	for _, m := range l.Models {
		for _, f := range m.Runtime {
			if !listed(files, f.Name) {
				files = append(files, f)
			}
		}
	}
	return files
}

// listed says whether a file of this name is among files.
func listed(files []ModelFile, name string) bool {
	return slices.ContainsFunc(files, func(f ModelFile) bool { return f.Name == name })
}

// sets are every file a voice needs, by directory: the runtime (what every voice needs and what
// this one adds) and its own.
func (l Library) sets(m Model) []fileSet {
	sets := []fileSet{{RuntimeID, slices.Concat(l.Runtime, m.Runtime)}}
	return append(sets, fileSet{m.ID, m.Files})
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
	m, ok := l.Find(id)
	if !ok {
		return false
	}
	for _, set := range l.sets(m) {
		if !l.present(set.dir, set.files) {
			return false
		}
	}
	return true
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

// DownloadSize is how many bytes downloading a voice takes besides the runtime every voice needs:
// its own files, and what it adds to the runtime while that is missing.
func (l Library) DownloadSize(m Model) int64 {
	n := m.Size()
	for _, f := range m.Runtime {
		if !l.present(RuntimeID, []ModelFile{f}) {
			n += f.Size
		}
	}
	return n
}

// RuntimeSize is how many bytes the shared files need.
func (l Library) RuntimeSize() int64 {
	return Model{Files: l.Runtime}.Size()
}

// File is the path of one file of a voice (or of RuntimeID), when it is on
// disk. Only names the catalog lists are answered, so no name can reach outside the directory.
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

// Sum is the SHA-256 the catalog pins for one file of a voice (or of RuntimeID), empty for a
// name it does not list. The page keeps its own copy of each file and asks again with it, so a
// file a newer catalog pins anew is fetched again and an unchanged one is not.
func (l Library) Sum(id, name string) string {
	files, _ := l.files(id)
	for _, f := range files {
		if f.Name == name {
			return f.SHA256
		}
	}
	return ""
}

// Sample is the path of a voice's sample, downloaded (and checked) the first time it is asked
// for. It is kept apart from the voice, which need not be downloaded.
func (l Library) Sample(ctx context.Context, id string) (string, error) {
	m, ok := l.Find(id)
	if !ok {
		return "", ErrUnknownModel
	}
	if m.Sample == nil {
		return "", ErrNoSample
	}
	f := *m.Sample
	f.Name = m.ID + filepath.Ext(f.Name)
	dir := filepath.Join(l.Dir, samplesDir)
	if !l.present(samplesDir, []ModelFile{f}) {
		noProgress := func(int64) {
			// A sample is a few seconds of audio: its download is not reported.
		}
		if err := l.fetch(ctx, f.URL, dir, []ModelFile{f}, noProgress); err != nil {
			return "", err
		}
	}
	return filepath.Join(l.Dir, samplesDir, f.Name), nil
}

// Install downloads what a voice is missing, the runtime included. progress, when not nil, is
// called with the bytes kept so far and the bytes this install needs.
func (l Library) Install(ctx context.Context, id string, progress func(done, total int64)) error {
	m, ok := l.Find(id)
	if !ok {
		return ErrUnknownModel
	}
	needs, total := l.missing(m)
	var done int64
	report := func(n int64) {
		done += n
		if progress != nil {
			progress(done, total)
		}
	}
	report(0)
	// One request per address and directory: the two phonemizer files come out of the same
	// archive, and so does onnxruntime's WebGPU build.
	for len(needs) > 0 {
		first := needs[0]
		var batch []ModelFile
		var rest []need
		for i, n := range needs {
			if i == 0 || first.joins(n) {
				batch = append(batch, n.f)
				continue
			}
			rest = append(rest, n)
		}
		if err := l.fetch(ctx, first.f.URL, filepath.Join(l.Dir, first.dir), batch, report); err != nil {
			return err
		}
		needs = rest
	}
	return nil
}

// need is a file an install is missing, with the directory it belongs to.
type need struct {
	dir string
	f   ModelFile
}

// joins says whether n comes in the same request as first: the same address into the same
// directory.
func (first need) joins(n need) bool {
	return n.f.URL == first.f.URL && n.dir == first.dir
}

// missing lists the files of m's sets that are not on disk, and how many bytes they add up to.
func (l Library) missing(m Model) ([]need, int64) {
	var needs []need
	var total int64
	for _, set := range l.sets(m) {
		for _, f := range set.files {
			if !l.present(set.dir, []ModelFile{f}) {
				needs = append(needs, need{set.dir, f})
				total += f.Size
			}
		}
	}
	return needs, total
}

// Remove deletes a voice's files. The runtime stays: another voice may use it.
func (l Library) Remove(id string) error {
	if _, ok := l.Find(id); !ok {
		return ErrUnknownModel
	}
	return os.RemoveAll(filepath.Join(l.Dir, id))
}

// Prune deletes what the catalog no longer has: the directories of voices it dropped (the
// Supertonic and Chatterbox ones, say), and their samples. A voice taken out of the catalog
// leaves nothing behind.
func (l Library) Prune() error {
	keep := map[string]bool{RuntimeID: true, samplesDir: true}
	samples := map[string]bool{}
	for _, m := range l.Models {
		keep[m.ID] = true
		if m.Sample != nil {
			samples[m.ID+filepath.Ext(m.Sample.Name)] = true
		}
	}
	entries, err := os.ReadDir(l.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		switch {
		case !keep[e.Name()]:
			errs = append(errs, os.RemoveAll(filepath.Join(l.Dir, e.Name())))
		case e.Name() != RuntimeID && e.Name() != samplesDir:
			errs = append(errs, l.adopt(e.Name()))
		default:
			// The runtime and the samples are where they belong.
		}
	}
	// No samples directory yet is nothing to prune.
	kept, _ := os.ReadDir(filepath.Join(l.Dir, samplesDir))
	for _, e := range kept {
		if !samples[e.Name()] {
			errs = append(errs, os.RemoveAll(filepath.Join(l.Dir, samplesDir, e.Name())))
		}
	}
	return errors.Join(errs...)
}

// adopt moves into RuntimeID the runtime files an earlier catalog kept in a voice's own
// directory (onnxruntime's WebGPU build, once one copy per model that used it), so a voice
// downloaded then stays downloaded. Its hash was checked when it was kept, as for any file
// on disk; a copy RuntimeID already has, or one of another size, is removed.
func (l Library) adopt(id string) error {
	own, _ := l.files(id)
	var errs []error
	for _, f := range l.runtimeFiles() {
		old := filepath.Join(l.Dir, id, f.Name)
		st, err := os.Stat(old)
		if err != nil || listed(own, f.Name) {
			continue
		}
		if st.Size() == f.Size && !l.present(RuntimeID, []ModelFile{f}) {
			errs = append(errs, os.MkdirAll(filepath.Join(l.Dir, RuntimeID), 0o700),
				os.Rename(old, filepath.Join(l.Dir, RuntimeID, f.Name)))
			continue
		}
		errs = append(errs, os.Remove(old))
	}
	return errors.Join(errs...)
}

// Verify hashes a voice's files on disk again, the runtime's included, and
// says which one is missing or no longer matches.
func (l Library) Verify(id string) error {
	m, ok := l.Find(id)
	if !ok {
		return ErrUnknownModel
	}
	for _, set := range l.sets(m) {
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
