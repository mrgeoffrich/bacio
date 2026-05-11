package inputs

// FeatureAddInput is the payload for `bacio feature add --json`.
type FeatureAddInput struct {
	Title       string `json:"title"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
}

// FeatureEditInput is the payload for `bacio feature edit --json`.
//
//   - title       absent = no change; "" or null = invalid
//   - description absent = no change; "" or null = clear
type FeatureEditInput struct {
	Slug        string  `json:"slug"`
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
}

// FeatureRmInput is the payload for `bacio feature rm --json`.
type FeatureRmInput struct {
	Slug string `json:"slug"`
}
