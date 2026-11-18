package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Release is the newest release of the configured channel with its
// manifest, already checked.
type Release struct {
	Tag         string
	Name        string
	PublishedAt string
	Author      string
	Manifest    *Manifest
	// Payload is the canonical text the signature covers.
	Payload []byte
	// Signature is manifest.sig as downloaded, nil when the release has
	// none.
	Signature []byte
	Trust     Trust
	// Asset is the manifest's entry for this platform's binary, nil when
	// the release has none; AssetURL and AssetSize come from GitHub's
	// list of the release's files.
	Asset     *ManifestFile
	AssetURL  string
	AssetSize int64
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Draft       bool   `json:"draft"`
	PublishedAt string `json:"published_at"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
	Assets []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}

var reTag = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// releasePath is where the API keeps the channel's release: the newest
// release that is not a pre-release for stable, the pre-release tagged
// "dev" for development.
func releasePath(owner, repo, branch string) string {
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases/"
	if branch == BranchDevelopment {
		return base + "tags/dev"
	}
	return base + "latest"
}

// lookupRelease finds the channel's release, downloads its manifest and
// signature, checks the manifest and judges the signature. tag, when set,
// asks for that release instead (a repair).
func (s *Service) lookupRelease(ctx context.Context, tag string, bypassCache bool) (*Release, error) {
	owner, repo, branch := s.Owner(), s.Repo(), s.Branch()
	path := releasePath(owner, repo, branch)
	if tag != "" {
		path = "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/releases/tags/" + url.PathEscape(tag)
	}
	body, err := s.gh.apiGet(ctx, path, bypassCache)
	if err != nil {
		return nil, err
	}
	var gr githubRelease
	if err := json.Unmarshal(body, &gr); err != nil {
		return nil, errors.New("Invalid JSON from GitHub API")
	}
	if !reTag.MatchString(gr.TagName) || gr.Draft {
		return nil, fmt.Errorf("GitHub release %q is not a published release", gr.TagName)
	}
	rel := &Release{Tag: gr.TagName, Name: gr.Name, PublishedAt: gr.PublishedAt, Author: gr.Author.Login}
	assets := map[string]githubAsset{}
	for _, a := range gr.Assets {
		if _, dup := assets[a.Name]; !dup {
			assets[a.Name] = a
		}
	}
	file := func(name string) releaseFile {
		return releaseFile{owner: owner, repo: repo, tag: gr.TagName, name: name}
	}

	manifestAsset, ok := assets["manifest.json"]
	if !ok {
		return nil, errNoManifest
	}
	var buf bytes.Buffer
	if _, err := s.gh.download(ctx, manifestAsset.URL, file("manifest.json"), maxManifestBytes, &buf); err != nil {
		return nil, err
	}
	manifest, err := ParseManifest(buf.Bytes(), ManifestRules{Owner: owner, Repo: repo, Branch: branch})
	if err != nil {
		return nil, err
	}
	rel.Manifest = manifest
	rel.Payload = CanonicalPayload(manifest.Signed())

	if sigAsset, ok := assets["manifest.sig"]; ok {
		var sig bytes.Buffer
		if _, err := s.gh.download(ctx, sigAsset.URL, file("manifest.sig"), maxSignatureBytes, &sig); err != nil {
			return nil, err
		}
		rel.Signature = sig.Bytes()
	}
	rel.Trust = EvaluateTrust(rel.Payload, rel.Signature, s.keys(), signingRequired(s.getenv(EnvRequireSigning)))

	name := AssetName(s.opts.GOOS, s.opts.GOARCH)
	if entry := manifest.File(name); entry != nil {
		if a, ok := assets[name]; ok {
			rel.Asset, rel.AssetURL, rel.AssetSize = entry, a.URL, a.Size
		}
	}
	return rel, nil
}

// keys are the compiled-in keys and those of UPDATE_MANIFEST_PUBLIC_KEY.
func (s *Service) keys() Keys {
	keys := append(Keys{}, s.opts.Keys...)
	return append(keys, ParseKeys(s.getenv(EnvPublicKey))...)
}

// downloadLimit is the most a binary may be: the size the manifest
// names, else the size GitHub lists, never more than MaxBinaryBytes.
func (r *Release) downloadLimit() int64 {
	limit := int64(MaxBinaryBytes)
	if r.Asset != nil && r.Asset.Size > 0 && r.Asset.Size < limit {
		limit = r.Asset.Size
	} else if r.AssetSize > 0 && r.AssetSize < limit {
		limit = r.AssetSize
	}
	return limit
}

// MaxBinaryBytes caps the size of a downloaded binary.
const MaxBinaryBytes = 512 << 20

// headline is the first line of a text.
func headline(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSuffix(line, "\r")
}
