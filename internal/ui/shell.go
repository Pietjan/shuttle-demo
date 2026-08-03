package ui

import (
	"context"
	"io"

	"github.com/pietjan/shuttle"
)

// Shell renders the document around the console.
//
// A Shell owns the whole page, and owes shuttle two things back or the page
// does not work:
//
//   - Page.Attach in a data-init attribute *on <body>*. Anything inside the
//     component's own markup is re-initialised by every patch, which would
//     open a second stream per patch.
//   - Page.Scripts somewhere in the document. That is the popstate shim -
//     the back and forward buttons are the only part of navigation the
//     server cannot see, so a shell that drops it loses them silently.
func Shell(w io.Writer, p shuttle.Page) error {
	return shellDocument(p).Render(context.Background(), w)
}
