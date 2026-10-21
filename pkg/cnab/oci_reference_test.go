package cnab

import (
	"encoding/json"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOCIReference(t *testing.T) {
	tests := []struct {
		Name               string
		Reference          OCIReference
		ExpectedTag        string
		ExpectedDigest     string
		ExpectedVersion    string
		ExpectedRepository string
		ExpectedRegistry   string
	}{
		{
			Name:               "Tag and digest specified",
			Reference:          MustParseOCIReference("carolynvs/whalesayd:v0.1.1@sha256:5cca9dfa8ba540a32537d586651d3918d6f39761cdf4457fbe32c58c36c1defc"),
			ExpectedRegistry:   "docker.io",
			ExpectedRepository: "carolynvs/whalesayd",
			ExpectedTag:        "v0.1.1",
			ExpectedDigest:     "sha256:5cca9dfa8ba540a32537d586651d3918d6f39761cdf4457fbe32c58c36c1defc",
			ExpectedVersion:    "0.1.1",
		},
		{
			Name:               "valid digested reference",
			Reference:          MustParseOCIReference("jeremyrickard/porter-do-bundle@sha256:a808aa4e3508d7129742eefda938249574447cce5403dc12d4cbbfe7f4f31e58"),
			ExpectedRegistry:   "docker.io",
			ExpectedRepository: "jeremyrickard/porter-do-bundle",
			ExpectedDigest:     "sha256:a808aa4e3508d7129742eefda938249574447cce5403dc12d4cbbfe7f4f31e58",
		},
		{
			Name:               "tagged reference",
			Reference:          MustParseOCIReference("jeremyrickard/porter-do-bundle:v0.1.0-pre_build.123"),
			ExpectedRegistry:   "docker.io",
			ExpectedRepository: "jeremyrickard/porter-do-bundle",
			ExpectedTag:        "v0.1.0-pre_build.123",
			ExpectedVersion:    "0.1.0-pre+build.123",
		},
		{
			Name:               "no tag",
			Reference:          MustParseOCIReference("ghcr.io/jeremyrickard/porter-do-bundle"),
			ExpectedRepository: "ghcr.io/jeremyrickard/porter-do-bundle",
			ExpectedRegistry:   "ghcr.io",
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			assert.Equal(t, test.ExpectedRepository, test.Reference.Repository())

			assert.Equal(t, test.ExpectedRegistry, test.Reference.Registry())

			assert.Equal(t, test.ExpectedDigest, test.Reference.Digest().String())
			assert.Equal(t, test.ExpectedDigest != "", test.Reference.HasDigest())

			assert.Equal(t, test.ExpectedTag, test.Reference.Tag())
			assert.Equal(t, test.ExpectedTag != "", test.Reference.HasTag())

			assert.Equal(t, test.ExpectedVersion, test.Reference.Version())
			assert.Equal(t, test.ExpectedVersion != "", test.Reference.HasVersion())
		})
	}
}

func TestParseOCIReference(t *testing.T) {
	testCases := []struct {
		Name           string
		Reference      string
		WantRepository string
		WantTag        string
		WantDigest     digest.Digest
		WantErr        string
	}{
		{Name: "version", Reference: "ghcr.io/getporter/examples/porter-hello:v0.2.0", WantRepository: "ghcr.io/getporter/examples/porter-hello", WantTag: "v0.2.0"},
		{Name: "digest", Reference: "ghcr.io/getporter/examples/porter-hello@sha256:88d68ef0bdb9cedc6da3a8e341a33e5d2f8bb19d0cf7ec3f1060d3f9eb73cae9", WantRepository: "ghcr.io/getporter/examples/porter-hello", WantDigest: "sha256:88d68ef0bdb9cedc6da3a8e341a33e5d2f8bb19d0cf7ec3f1060d3f9eb73cae9"},
		{Name: "invalid", Reference: "@v1", WantErr: "invalid reference format"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			reference, err := ParseOCIReference(testCase.Reference)
			if testCase.WantErr != "" {
				require.Error(t, err, "expected ParseOCIReference to fail")
				assert.Contains(t, err.Error(), testCase.WantErr)
			} else {
				require.NoError(t, err, "expected ParseOCIReference to succeed")
				assert.Equal(t, testCase.WantRepository, reference.Repository(), "incorrect repo")
				assert.Equal(t, testCase.WantTag, reference.Tag(), "incorrect tag")
				assert.Equal(t, testCase.WantDigest, reference.Digest(), "incorrect digest")
			}
		})
	}
}

func TestOCIReference_MarshalJSON(t *testing.T) {
	reference := MustParseOCIReference("ghcr.io/getporter/examples/porter-hello:v0.2.0")
	data, err := json.Marshal(reference)
	require.NoError(t, err)
	assert.Equal(t, `"ghcr.io/getporter/examples/porter-hello:v0.2.0"`, string(data))
}

func TestOCIReference_UnmarshalJSON(t *testing.T) {
	reference := `"ghcr.io/getporter/examples/porter-hello:v0.2.0"`
	var ociReference OCIReference
	err := json.Unmarshal([]byte(reference), &ociReference)
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/getporter/examples/porter-hello:v0.2.0", ociReference.String())
}

func TestOCIReference_WithVersion(t *testing.T) {
	t.Run("prefixed semver", func(t *testing.T) {
		reference := MustParseOCIReference("getporter/porter-hello")

		result, err := reference.WithVersion("v1.2.3")
		require.NoError(t, err)
		assert.Equal(t, "v1.2.3", result.Tag())
	})

	t.Run("unprefixed semver", func(t *testing.T) {
		reference := MustParseOCIReference("getporter/porter-hello")

		result, err := reference.WithVersion("1.2.3")
		require.NoError(t, err)
		assert.Equal(t, "v1.2.3", result.Tag())
	})

	t.Run("semver with build metadata", func(t *testing.T) {
		reference := MustParseOCIReference("getporter/porter-hello")

		result, err := reference.WithVersion("1.2.3+git0a2b3c4d")
		require.NoError(t, err)
		assert.Equal(t, "v1.2.3_git0a2b3c4d", result.Tag())
	})

	t.Run("invalid semver", func(t *testing.T) {
		reference := MustParseOCIReference("getporter/porter-hello")

		_, err := reference.WithVersion("oops")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid bundle version")
	})
}

func TestOCIReference_WithTag(t *testing.T) {
	reference := MustParseOCIReference("getporter/porter-hello")

	result, err := reference.WithTag("latest")
	require.NoError(t, err)
	assert.Equal(t, "latest", result.Tag())
}

func TestOCIReference_WithDigest(t *testing.T) {
	t.Run("valid digest", func(t *testing.T) {
		reference := MustParseOCIReference("getporter/porter-hello")

		result, err := reference.WithDigest("sha256:a808aa4e3508d7129742eefda938249574447cce5403dc12d4cbbfe7f4f31e58")
		require.NoError(t, err)
		assert.Equal(t, "sha256:a808aa4e3508d7129742eefda938249574447cce5403dc12d4cbbfe7f4f31e58", result.Digest().String())
	})

	t.Run("invalid digest", func(t *testing.T) {
		reference := MustParseOCIReference("getporter/porter-hello")

		_, err := reference.WithDigest("oops")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid digest")
	})
}
