// Package docsync keeps what the repository says about itself true.
//
// Sibling of specsync: that one ties features/ to the tests, this one ties
// prose to the files it describes — the CLAUDE.md map, the landing page's FAQ
// and its JSON-LD copy, the version tooling and the release workflow. Prose
// about behaviour rots like code, without a compiler to notice.
//
// Only claims a machine can decide belong here. A rule that needs judgement
// stays a sentence in CLAUDE.md; a checker that cries wolf gets ignored. When
// a prose rule has been broken twice, it becomes a test in this package
// (CLAUDE.md §1).
package docsync
