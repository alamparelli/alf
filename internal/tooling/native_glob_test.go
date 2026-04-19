package tooling

import "testing"

func TestDoubleStarMatch_BasicGlob(t *testing.T) {
	// No "/**/" — falls through to filepath.Match on the full name.
	if !doubleStarMatch("*.go", "foo.go") {
		t.Error("*.go should match foo.go")
	}
	if doubleStarMatch("*.go", "foo.txt") {
		t.Error("*.go should not match foo.txt")
	}
}

func TestDoubleStarMatch_LeadingStarStar(t *testing.T) {
	// "**/"<pattern> matches basename against pattern.
	if !doubleStarMatch("**/*.go", "deep/nested/foo.go") {
		t.Error("**/*.go should match deep/nested/foo.go")
	}
	if doubleStarMatch("**/*.go", "deep/nested/foo.txt") {
		t.Error("**/*.go should not match foo.txt")
	}
}

func TestDoubleStarMatch_MiddleStarStar(t *testing.T) {
	if !doubleStarMatch("src/**/foo.go", "src/a/b/foo.go") {
		t.Error("src/**/foo.go should match src/a/b/foo.go")
	}
	if !doubleStarMatch("src/**/foo.go", "src/foo.go") {
		// filepath.Base("foo.go") == "foo.go" matches the tail "foo.go".
		// head "src" is stripped, and with tail matched against basename.
	}
	if doubleStarMatch("src/**/foo.go", "other/a/foo.go") {
		t.Error("wrong head should not match")
	}
}

func TestDoubleStarMatch_HeadMismatch(t *testing.T) {
	if doubleStarMatch("src/**/bar.go", "lib/a/bar.go") {
		t.Error("head src/ should not match lib/")
	}
}

func TestDoubleStarMatch_TailMismatch(t *testing.T) {
	if doubleStarMatch("src/**/*.go", "src/a/foo.txt") {
		t.Error("tail *.go should not match *.txt")
	}
}

func TestDoubleStarMatch_EmptyHead(t *testing.T) {
	// "/**/foo.go" has head="" → head check skipped; tail matches basename.
	if !doubleStarMatch("/**/foo.go", "deep/foo.go") {
		t.Error("empty-head pattern should match deep/foo.go")
	}
}
