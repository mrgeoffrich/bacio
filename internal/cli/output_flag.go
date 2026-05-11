package cli

import "fmt"

// outputFlag implements pflag.Value so cobra can validate at parse time.
type outputFlag struct {
	target *outputFormat
}

func newOutputFlag(target *outputFormat) *outputFlag {
	return &outputFlag{target: target}
}

func (f *outputFlag) String() string {
	if f.target == nil || *f.target == "" {
		return string(outputText)
	}
	return string(*f.target)
}

func (f *outputFlag) Set(s string) error {
	switch outputFormat(s) {
	case outputText, outputJSON:
		*f.target = outputFormat(s)
		return nil
	default:
		return fmt.Errorf("invalid value %q (valid: text, json)", s)
	}
}

func (f *outputFlag) Type() string { return "text|json" }
