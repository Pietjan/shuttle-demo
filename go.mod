module github.com/pietjan/shuttle-quickstart

go 1.26.5

// Neither library has a tagged release yet, so both resolve from the working
// copies beside this one. When shuttle tags a version, drop both lines and
// `go get github.com/pietjan/shuttle@vX.Y.Z` - and remember that
// assets/css/input.css points @source at the same directory, so that path
// becomes a module-cache path at the same time.
replace github.com/pietjan/shuttle => ../shuttle

replace github.com/pietjan/loom => ../loom

require (
	github.com/a-h/templ v0.3.1020
	github.com/pietjan/loom v0.0.0
	github.com/pietjan/shuttle v0.0.0-00010101000000-000000000000
)

require (
	github.com/CAFxX/httpcompression v0.0.9 // indirect
	github.com/Oudwins/tailwind-merge-go v0.2.3 // indirect
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/starfederation/datastar-go v1.2.2 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	golang.org/x/net v0.57.0 // indirect
)
