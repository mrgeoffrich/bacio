package channel

import "fmt"

// TranscriptCap caps how much raw subagent transcript (.jsonl) we keep
// on a document attachment before truncating. It is deliberately well
// under the store's ValidateBody doc-body cap so a transcript full of
// huge file reads can never push the attachment past what a doc
// accepts. 2.5 MB — a clean 10x of the 256 KB digest cap it replaced
// (BACI-90).
const TranscriptCap = 2560 * 1024

// CapTranscript returns raw unchanged when it fits TranscriptCap.
// Otherwise it returns the first TranscriptCap bytes (minus room for a
// footer) followed by a one-line truncation footer naming the omitted
// byte count and the source path. The footer is appended after a
// newline so it lands on its own line; it is intentionally NOT valid
// JSON — it is a human marker, acceptable because the attachment is the
// raw, unformatted .jsonl per BACI-90. The returned slice never exceeds
// TranscriptCap.
func CapTranscript(raw []byte, sourcePath string) []byte {
	if len(raw) <= TranscriptCap {
		return raw
	}
	footerFor := func(omitted int) string {
		return fmt.Sprintf(
			"\n[bacio: transcript truncated — %d of %d bytes omitted to keep the attachment under %d KB. Full raw transcript at %s]\n",
			omitted, len(raw), TranscriptCap/1024, sourcePath)
	}
	// The footer embeds the omitted count, whose digit-length depends
	// on the cut point, which in turn depends on the footer length.
	// Size the footer for the worst case (omitted == len(raw), the
	// most digits it can ever have), so the final artefact is
	// guaranteed <= TranscriptCap regardless of the real omitted count.
	footerBudget := len(footerFor(len(raw)))
	keep := TranscriptCap - footerBudget
	if keep < 0 {
		keep = 0
	}
	omitted := len(raw) - keep
	out := make([]byte, 0, keep+footerBudget)
	out = append(out, raw[:keep]...)
	out = append(out, footerFor(omitted)...)
	return out
}
