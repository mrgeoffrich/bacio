package inputs

// Doc-folder payloads.
//
// Folders are addressed on the CLI by their slash-separated DISPLAY PATH
// ("Design/API/Auth"), never by uuid or id: a path is what a human types
// and what `bacio doc folder list` prints. The CLI resolves the path to
// the folder's uuid off one `ListDocFolders` round trip before calling
// the client, so the uuid-addressed contract underneath is unchanged.
//
// The EMPTY PATH is meaningful, not missing: "" is the TREE ROOT. The
// root is not itself a folder, so there is no path that names it other
// than "". Segment matching is case-sensitive and exact — a folder named
// "Design" is not reachable as "design".

// DocFolderAddInput is the payload for `bacio doc folder add --json`.
// Parent is the slash path of the containing folder; omit it (or pass
// "") to create the folder at the tree root.
type DocFolderAddInput struct {
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"`
}

// DocFolderRenameInput is the payload for `bacio doc folder rename
// --json`. Path names the folder to rename; Name is the new leaf name
// (a single segment — renaming does not move the folder, so Name must
// not contain a separator).
type DocFolderRenameInput struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// DocFolderMvInput is the payload for `bacio doc folder mv --json` — a
// re-parent, which carries the folder's whole subtree with it.
//
// `to` is REQUIRED but may be empty: "" re-roots the folder at the top
// of the tree. Because "" is a real destination, the decoder checks the
// key's PRESENCE rather than its emptiness — omitting `to` is an error,
// passing `"to": ""` is the documented way to say "the tree root".
//
// Cycles (moving a folder inside its own subtree) and the depth cap are
// enforced inside the store transaction, so they surface on the commit
// path even if a `--dry-run` projected the move.
type DocFolderMvInput struct {
	Path string `json:"path"`
	To   string `json:"to"`
}

// DocFolderRmInput is the payload for `bacio doc folder rm --json`.
// Descendant folders go with it; every document anywhere in the subtree
// is RE-ROOTED, never deleted. Rehearse with `--dry-run` to see both
// counts before committing.
type DocFolderRmInput struct {
	Path string `json:"path"`
}

// DocMvInput is the payload for `bacio doc mv --json` — file a document
// into a folder.
//
// `folder` is REQUIRED but may be empty: "" files the document at the
// tree root (the way to un-file a page). As with DocFolderMvInput the
// decoder checks the key's presence, so omitting it is an error.
//
// Position is a loose SORT KEY, not a dense index — siblings may share
// one, and listings tie-break on filename. Omit it to append after the
// target folder's current members.
type DocMvInput struct {
	Filename string `json:"filename"`
	Folder   string `json:"folder"`
	Position *int   `json:"position,omitempty"`
}
