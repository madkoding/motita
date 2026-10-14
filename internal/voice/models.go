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

// LOCAL VOICES: Piper and Kokoro models the browser runs itself.
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
	// Engine is what runs the voice in the page: EnginePiper or EngineKokoro.
	Engine string `json:"engine"`
	// Shared is the id of the files this voice uses with the others of its engine (Kokoro's
	// model), downloaded with the first of them; "" when it has none.
	Shared string `json:"-"`
	// Files are the voice's own: a Piper model and its settings (the .onnx.json the page reads
	// the phonemizer voice and the sample rate from), or a Kokoro voice's style vectors.
	Files []ModelFile `json:"files"`
	// Sample is a short recording of the voice, heard before it is downloaded. Nil when the
	// catalog has none.
	Sample *ModelFile `json:"-"`
}

// The engines a local voice runs on.
const (
	EnginePiper  = "piper"
	EngineKokoro = "kokoro"
)

// RuntimeID is the directory, and the id in a file's URL, of the files every voice shares.
const RuntimeID = "runtime"

// KokoroID is the directory, and the id in a file's URL, of the model every Kokoro voice shares.
// The page knows a Kokoro voice by its id, which starts with KokoroID and a dash.
const KokoroID = "kokoro"

// samplesDir is the directory the samples are kept in, apart from the voices.
const samplesDir = "samples"

const (
	piperVoices = "https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/"
	// The runtime comes from the npm packages the page is built with: the versions here must
	// be the ones web/package.json pins, or the bundled code and the binary it loads disagree.
	ortPackage   = "https://registry.npmjs.org/onnxruntime-web/-/onnxruntime-web-1.30.0.tgz"
	piperPackage = "https://registry.npmjs.org/@diffusionstudio/piper-wasm/-/piper-wasm-1.0.0.tgz"
	// kokoroRepo is onnx-community's ONNX export of Kokoro-82M v1.0, at a fixed commit.
	kokoroRepo = "https://huggingface.co/onnx-community/Kokoro-82M-v1.0-ONNX/resolve/1939ad2a8e416c0acfeecc08a694d14ef25f2231/"
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

// piperVoice is one voice of rhasspy/piper-voices, by its path there, with the sample the
// repository keeps next to it.
func piperVoice(path, name, lang, quality string, size int64, sum string, jsonSize int64, jsonSum string,
	sampleSize int64, sampleSum string) Model {
	cut := strings.LastIndex(path, "/") + 1
	return Model{ID: path[cut:], Name: name, Lang: lang, Quality: quality, Engine: EnginePiper,
		Files: []ModelFile{
			{Name: "model.onnx", URL: piperVoices + path + ".onnx", Size: size, SHA256: sum},
			{Name: "model.json", URL: piperVoices + path + ".onnx.json", Size: jsonSize, SHA256: jsonSum},
		},
		Sample: &ModelFile{Name: "sample.mp3", URL: piperVoices + path[:cut] + "samples/speaker_0.mp3",
			Size: sampleSize, SHA256: sampleSum}}
}

// KokoroModel is the model every Kokoro voice runs on (8-bit: onnxruntime's WebAssembly runs it
// as fast as the full one, at a third of the size), and its table from phonemes to ids.
var KokoroModel = []ModelFile{
	{Name: "model.onnx", URL: kokoroRepo + "onnx/model_quantized.onnx",
		Size: 92361116, SHA256: "fbae9257e1e05ffc727e951ef9b9c98418e6d79f1c9b6b13bd59f5c9028a1478"},
	{Name: "tokenizer.json", URL: kokoroRepo + "tokenizer.json",
		Size: 3497, SHA256: "77a02c8e164413299b4b4c403b14f8e0e1c1b727db4d46a09d6327b861060a34"},
}

// kokoroVoice is one voice of Kokoro, by its name there. The name's first letter is its language
// (a American English, b British English, e Spanish), and the page reads it from the id.
func kokoroVoice(voice, name, lang, sum string) Model {
	return Model{ID: KokoroID + "-" + voice, Name: name, Lang: lang, Quality: "high", Engine: EngineKokoro,
		Shared: KokoroID, Files: []ModelFile{
			{Name: "voice.bin", URL: kokoroRepo + "voices/" + voice + ".bin", Size: 522240, SHA256: sum},
		}}
}

// KokoroVoices are the Kokoro voices offered: the best graded of each language and gender.
var KokoroVoices = []Model{
	kokoroVoice("ef_dora", "Dora", "es", "f66ec66bd295acb18372e37008533a9a3228483ccd294e7538d5d9294ac9a532"),
	kokoroVoice("em_alex", "Alex", "es", "27809e9eafdcbcfff90a3016c697568676531de2a2c39cee29c96c7bd6b83e95"),
	kokoroVoice("af_heart", "Heart", "en-US", "d583ccff3cdca2f7fae535cb998ac07e9fcb90f09737b9a41fa2734ec44a8f0b"),
	kokoroVoice("af_bella", "Bella", "en-US", "f69d836209b78eb8c66e75e3cda491e26ea838a3674257e9d4e5703cbaf55c8b"),
	kokoroVoice("af_nicole", "Nicole", "en-US", "cd2191ab31b914ed7b318416b0e4440fdf392ddad9106a060819aa600a64f59a"),
	kokoroVoice("am_michael", "Michael", "en-US", "1d1f21dd8da39c30705cd4c75d039d265e9bc4a2a93ed09bc9e1b1225eb95ba1"),
	kokoroVoice("am_fenrir", "Fenrir", "en-US", "c27989f741f7ee34d273a39d8a595cc0837d35f5ced9a29b7cc162614616df43"),
	kokoroVoice("bf_emma", "Emma", "en-GB", "669fe0647f9dd04fcab92f1439a40eeb4c8b4ab1f82e4996fe3d918ce4a63b73"),
	kokoroVoice("bm_george", "George", "en-GB", "c4b235a4c1f2cd3b939fed08b899ce9385638b763f7b73a59616c4fc9bd6c9bc"),
}

// PiperVoices is the catalog: Spanish and English voices in every quality, from the 14 MB x-low
// to the 114 MB high, the hashes those of the v1.0.0 tag of rhasspy/piper-voices.
var PiperVoices = []Model{
	piperVoice("es/es_MX/claude/high/es_MX-claude-high", "Claude", "es-MX", "high",
		63122309, "3ef40a71ea63852cd8ab7e6fa7d2ecdcfa67a0b47c9c48e3f10e02ee02083ea0",
		4963, "1afc81f703c0e4cb3b4d7c0dca096b8b54a98806807f0170cf5eb5557723c12d",
		189537, "9022f6b03498baacffe28024506f40ac43fa5a6c533b6dd902c2f3767acbfc94"),
	piperVoice("es/es_MX/ald/medium/es_MX-ald-medium", "Ald", "es-MX", "medium",
		63201294, "019b3803293c93e34a206dd2e53a3889209a514e786fd7144f7b70196c579b63",
		4889, "efab736e62e5321dd5d063d1b46e63c59ce655419816355b81d025c1a8d6b03c",
		176745, "bec81447c21d5c4be7ed845ea2803982b36f5df198db8f76eb04f81b513fdb46"),
	piperVoice("es/es_ES/davefx/medium/es_ES-davefx-medium", "Davefx", "es-ES", "medium",
		63201294, "6658b03b1a6c316ee4c265a9896abc1393353c2d9e1bca7d66c2c442e222a917",
		4817, "0e0dda87c732f6f38771ff274a6380d9252f327dca77aa2963d5fbdf9ec54842",
		191084, "00740c7094a7464d5d085c8057e5299314d0f7d1930df6751ff97c483e5b3d9a"),
	piperVoice("en/en_US/lessac/medium/en_US-lessac-medium", "Lessac", "en-US", "medium",
		63201294, "5efe09e69902187827af646e1a6e9d269dee769f9877d17b16b1b46eeaaf019f",
		4885, "efe19c417bed055f2d69908248c6ba650fa135bc868b0e6abb3da181dab690a0",
		83453, "50952249253e66f8d448ace6a8b1ca32eee1209aa41a81898d83e71979c86c52"),
	piperVoice("en/en_US/amy/medium/en_US-amy-medium", "Amy", "en-US", "medium",
		63201294, "b3a6e47b57b8c7fbe6a0ce2518161a50f59a9cdd8a50835c02cb02bdd6206c18",
		4882, "95a23eb4d42909d38df73bb9ac7f45f597dbfcde2d1bf9526fdeaf5466977d77",
		95553, "89a70b1c5f88f395ca4f1fbf916b53a4f8f66c054bfb06bc3ab3b4520b68d1f7"),
	piperVoice("en/en_GB/alba/medium/en_GB-alba-medium", "Alba", "en-GB", "medium",
		63201294, "401369c4a81d09fdd86c32c5c864440811dbdcc66466cde2d64f7133a66ad03b",
		4888, "aa965a2f02ecced632c2694e1fc72bbff6d65f265fab567ca945918c73dd89f4",
		88476, "6df4f047daa0f7e44a6844216b26ceef3e1eabe2e314621b5b5fa28c6eb57d30"),
	piperVoice("es/es_AR/daniela/high/es_AR-daniela-high", "Daniela", "es-AR", "high",
		114199011, "7ceb1fc0dab349418c5b54a639ae9ee595212d7c9ea422220d8419163d5cc985",
		7248, "aedbf69647e1d754c62ecf8e0366ca5f16af3e768e3c6b5329af6eb6bde3852b",
		139548, "4d84c1235a1b61c0d764c544ad7a2ecc9e3ce3852cf31d1ba279479b0e27e959"),
	piperVoice("es/es_ES/sharvard/medium/es_ES-sharvard-medium", "Sharvard", "es-ES", "medium",
		76733615, "40febfb1679c69a4505ff311dc136e121e3419a13a290ef264fdf43ddedd0fb1",
		4903, "7438c9b699c72b0c3388dae1b68d3f364dc66a2150fe554a1c11f03372957b2c",
		164960, "e9a9b72e71cc15851b3c3f1c1593032feee6738528d225eb9512ee8a6128bd01"),
	piperVoice("es/es_ES/mls_10246/low/es_ES-mls_10246-low", "MLS 10246", "es-ES", "low",
		63104526, "3f9d76d2778778942297aab052aa7b2e67248d3f43614c889ee82901a230e197",
		4160, "a865944d7f6972af347263b0b8152466dabba3b78b8fe5a6985f7d3b19fd06a1",
		120357, "d068cd4f849fb00487d6ed5d32617fa29c33ca60bae5068c3f69fefbf6910e8b"),
	piperVoice("es/es_ES/mls_9972/low/es_ES-mls_9972-low", "MLS 9972", "es-ES", "low",
		63104526, "afdd61beb26d3aba18245c0352a8173e7f96196fbe4979e115590e8f775c95f0",
		4159, "32234a0701c480bb6ba62d4f05d0c03a95369f2133aa655370db1c00b2193340",
		140085, "38a5f294c2636c46908cb9092e28aca93abc1b5e90bc9a72367b8bdda7a3a6e3"),
	piperVoice("es/es_ES/carlfm/x_low/es_ES-carlfm-x_low", "Carlfm", "es-ES", "x-low",
		28130791, "d69677323a907cd4963f42b29c20a98b5d6bfa7f3e64df339915e4650c00d125",
		4159, "d9bdfa9ff01eb2bc9e62e7d2593939d1e4c4d8eb7cf75f972731539d12399966",
		126081, "76159c987bbf4f1d0142330800e43f740595c77fa34cc44c13857cadbe2225b6"),
	piperVoice("en/en_US/lessac/high/en_US-lessac-high", "Lessac", "en-US", "high",
		113895201, "4cabf7c3a638017137f34a1516522032d4fe3f38228a843cc9b764ddcbcd9e09",
		4883, "db42b97d9859f257bc1561b8ed980e7fb2398402050a74ddd6cbec931a92412f",
		78282, "eabd9e164e56e5b7d6ac36fecdb8c632e82f0fad7e73eb14a471ef95206f883d"),
	piperVoice("en/en_US/lessac/low/en_US-lessac-low", "Lessac", "en-US", "low",
		63201294, "f7d01dde371555732c4c314111ac79672b1a5ce2fc19266ab42178fd8df7f375",
		4882, "45754dfdebb3b8661c3fc564713772deec6e064feeb5b4e9594857dc7305193a",
		73089, "9fe62789fbd487bb967a73067a0e0eef92db1160533390e4a99c9fdfbabeaa3f"),
	piperVoice("en/en_US/amy/low/en_US-amy-low", "Amy", "en-US", "low",
		63104526, "a5a91abb7de0f104358a25aded480ddacf1ff0762886325886ec406a2e86aab3",
		4164, "2250a9a605b8dc35a116717fadc5056695dd809e34a15d02f72a0f52d53d3ebb",
		77193, "5aebce1dfd5cedd9d4740500aefddd042a18cf63d1cca97e110e89966107ba0e"),
	piperVoice("en/en_US/joe/medium/en_US-joe-medium", "Joe", "en-US", "medium",
		63201294, "58afce0321b8d9c46d7cdf9c16500cc55a793b4220212dba6b70fb788b3baf06",
		4794, "3d6d5410b3795cb1950595247ef8f06190719e6fdbfa3a2356d8ec368e1aad33",
		90499, "7391ee0a89f2fbf9d5dfb0e53314d32ee9b130a7ddfde1a456717668309e1829"),
	piperVoice("en/en_US/kristin/medium/en_US-kristin-medium", "Kristin", "en-US", "medium",
		63531379, "5849957f929cbf720c258f8458692d6103fff2f0e3d3b19c8259474bb06a18d4",
		4968, "5681426d4aead22195de70531eeeeddb46493cfaffc5764b2ea3db73428b651c",
		98782, "1dae7f1de1230ee75c89225af7a4dad4adb37097ae57c0e3ea8a812cab77f1e8"),
	piperVoice("en/en_US/hfc_female/medium/en_US-hfc_female-medium", "HFC female", "en-US", "medium",
		63201294, "914c473788fc1fa8b63ace1cdcdb44588f4ae523d3ab37df1536616835a140b7",
		5033, "03f1fa0622b80463283592d97aca9f6e89aec345a5c56b7257723e0093c58b6c",
		86872, "c9682897d836ca48bad1b49392261b1383038e7dfbe5e0c1a99664dce20a4fe3"),
	piperVoice("en/en_US/hfc_male/medium/en_US-hfc_male-medium", "HFC male", "en-US", "medium",
		63201294, "d11e403a02bdf5a670c877b3dc56e0e1c8cece6fb30289586314dffdc0a78cb0",
		5033, "f66847424aed0bf99ecbb5d7cfde47c0a906f426a0daf7c46f305e7d21afd886",
		93221, "2127dd9329b89d98c22bad87bd16c3f6c4aebb8d74323789fcd353fce5c313a6"),
	piperVoice("en/en_US/danny/low/en_US-danny-low", "Danny", "en-US", "low",
		63104526, "56a9ae9499e961514f060aac2866cb323a21e5989592fc4f208e56fdc323ab64",
		4166, "191dea1ba9863199d8ca2e9048f83b219219fb450f62767ee02f1bc4568ce4f4",
		68841, "8b7ce059a73f80b010f5d4969d39d43ace155e49fbe0442bd2005feeda9e072c"),
	piperVoice("en/en_GB/cori/high/en_GB-cori-high", "Cori", "en-GB", "high",
		114219352, "470b4dd634c98f8a4850d7626ffc3dfc90774628eeef6605a6dd8f88f30a5903",
		4963, "9e7fb5b5671612c22f3c81cbe46c1ae87b031a4632bcb509e499dad6f1e2adec",
		95280, "46ce2b83a7c717cd0e0cba19e339cbbfdeee092966241ab8f885fa54c68751f2"),
	piperVoice("en/en_GB/alan/medium/en_GB-alan-medium", "Alan", "en-GB", "medium",
		63201294, "0a309668932205e762801f1efc2736cd4b0120329622adf62be09e56339d3330",
		4888, "c0f0d124e5895c00e7c03b35dcc8287f319a6998a365b182deb5c8e752ee8c1e",
		102223, "d9014c8b391383884b297cf1af53c12be86393f9f05b9103760abfed2e9a4798"),
	piperVoice("en/en_GB/jenny_dioco/medium/en_GB-jenny_dioco-medium", "Jenny", "en-GB", "medium",
		63201294, "469c630d209e139dd392a66bf4abde4ab86390a0269c1e47b4e5d7ce81526b01",
		4895, "a9a7a93a317c9a3cb6563e37eb057df9ef09c06188a8a4341b0fcb58cba54dd4",
		95066, "72a3d7f34f93a531a4f07dafcafd08c5c27dd459f56072c748b5005c1c75ac98"),
	piperVoice("en/en_GB/northern_english_male/medium/en_GB-northern_english_male-medium", "Northern English male", "en-GB", "medium",
		63201294, "57a219ae8e638873db7d18893304be5069c42868f392bb95c3ff17f0690d0689",
		4847, "69557ed3d974463453e9b0c09dd99a7ed0e52b8b87b64b357dbeeb2540a97d47",
		87884, "b81a829cca988937a80c472bfaa5cfaf11c035eac3537a03481f54d0ab852c22"),
	piperVoice("en/en_GB/southern_english_female/low/en_GB-southern_english_female-low", "Southern English female", "en-GB", "low",
		63104526, "f2f37aed1b3a093476f719d1379ba0c0b1b1cf6f1ef99288e2ebf502971a07c3",
		4189, "a1af43310f8756161506e849a2863d342a98b0853af9e2437581d39e9922d0e5",
		69021, "f08db8ed3609286e01377111a065aeabbd60e9cb2308fc0d88a543495971333d"),
}

// Library is the local voices on this machine: the catalog, and the directory the downloaded
// files are kept in (one subdirectory per voice, plus RuntimeID and each shared set's id).
type Library struct {
	Dir     string
	Runtime []ModelFile
	// Shared are the files some voices share, by the id their Model.Shared names.
	Shared map[string][]ModelFile
	Models []Model
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

// files are the files of an id: the runtime's, a shared set's or a voice's.
func (l Library) files(id string) ([]ModelFile, bool) {
	if id == RuntimeID {
		return l.Runtime, true
	}
	if set, ok := l.Shared[id]; ok {
		return set, true
	}
	m, ok := l.Find(id)
	return m.Files, ok
}

// fileSet is the files kept in one directory.
type fileSet struct {
	dir   string
	files []ModelFile
}

// sets are every file a voice needs, by directory: the runtime, its shared set, its own.
func (l Library) sets(m Model) []fileSet {
	sets := []fileSet{{RuntimeID, l.Runtime}}
	if m.Shared != "" {
		sets = append(sets, fileSet{m.Shared, l.Shared[m.Shared]})
	}
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

// Installed says whether a voice can be used: its files, its shared set and the runtime are all
// on disk.
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

// DownloadSize is how many bytes downloading a voice takes besides the runtime: its own files,
// and its shared set while that is not on disk.
func (l Library) DownloadSize(m Model) int64 {
	n := m.Size()
	if m.Shared != "" && !l.present(m.Shared, l.Shared[m.Shared]) {
		n += Model{Files: l.Shared[m.Shared]}.Size()
	}
	return n
}

// RuntimeSize is how many bytes the shared files need.
func (l Library) RuntimeSize() int64 {
	return Model{Files: l.Runtime}.Size()
}

// File is the path of one file of a voice (or of RuntimeID, or of a shared set), when it is on
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
	if !l.present(samplesDir, []ModelFile{f}) {
		if err := l.fetch(ctx, f.URL, filepath.Join(l.Dir, samplesDir), []ModelFile{f}, func(int64) {}); err != nil {
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
	type need struct {
		dir string
		f   ModelFile
	}
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

// Remove deletes a voice's files. The runtime stays: another voice may use it. A shared set goes
// with the last voice of its engine that was on disk.
func (l Library) Remove(id string) error {
	m, ok := l.Find(id)
	if !ok {
		return ErrUnknownModel
	}
	if err := os.RemoveAll(filepath.Join(l.Dir, id)); err != nil {
		return err
	}
	if m.Shared == "" {
		return nil
	}
	for _, o := range l.Models {
		if o.Shared == m.Shared && l.present(o.ID, o.Files) {
			return nil
		}
	}
	return os.RemoveAll(filepath.Join(l.Dir, m.Shared))
}

// Verify hashes a voice's files on disk again, the runtime's and its shared set's included, and
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
