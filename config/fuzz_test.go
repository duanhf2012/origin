package config

import "testing"

func FuzzDuration(f *testing.F) {
	for _, seed := range []string{"0s", "15s", "2h30m", "", "-1s", "1s1m"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		var value Duration
		_ = value.UnmarshalText([]byte(input))
	})
}

func FuzzByteSize(f *testing.F) {
	for _, seed := range []string{"0B", "16B", "4M", "", "-1M", "1MiB"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		var value ByteSize
		_ = value.UnmarshalText([]byte(input))
	})
}

func FuzzExpandString(f *testing.F) {
	for _, seed := range []string{"plain", "${NAME}", "$${NAME}", "${", "${1BAD}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		_, _, _ = expandString(input, func(name string) (string, bool) {
			return "value", name == "NAME"
		})
	})
}

func FuzzParseData(f *testing.F) {
	f.Add("value: true\n", false)
	f.Add(`{"value":true}`, true)
	f.Add("value: [unterminated", false)
	f.Add(`{"value":}`, true)

	f.Fuzz(func(t *testing.T, input string, jsonFormat bool) {
		format := ".yaml"
		if jsonFormat {
			format = ".json"
		}
		_, _ = parseData(configFile{relative: "fuzz" + format, format: format}, []byte(input))
	})
}

func FuzzParseAndMerge(f *testing.F) {
	f.Add("first: true\n", "second: false\n")
	f.Add("items: [one]\n", "items: [two]\n")
	f.Add("value: true\n", "value: false\n")

	f.Fuzz(func(t *testing.T, first, second string) {
		left, leftErr := parseData(configFile{relative: "first.yaml", format: ".yaml"}, []byte(first))
		right, rightErr := parseData(configFile{relative: "second.yaml", format: ".yaml"}, []byte(second))
		if leftErr == nil && rightErr == nil {
			_ = mergeNodes(left, right, "")
		}
	})
}
