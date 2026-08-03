// Package assets carries what the browser needs, compiled into the binary.
//
// Embedding rather than serving from disk is what lets the container image
// be scratch: there is no filesystem in there to copy a stylesheet onto, and
// an app that reads one at startup would have to grow a base image to hold
// it.
//
// The sheet is built by `make css` and committed, because it is embedded -
// a missing file here is a compile error, not a page that renders unstyled.
package assets

import (
	_ "embed"
)

// Stylesheet is the compiled Tailwind output.
//
// It is compiled *for this application*, which is the part worth
// understanding rather than copying: loom ships markup, not CSS, so the
// classes baked into its components only exist in a sheet whose Tailwind
// build was pointed at loom's source - and shuttle's live/ kit adds a second
// directory to point at. assets/css/input.css has the details and the
// failure modes, which are all quiet ones.
//
//go:embed static/styles.css
var Stylesheet []byte
