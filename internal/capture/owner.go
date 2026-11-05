package capture

import "strings"

// Owner is the app a copy came from. App is the stable identifier the ignore
// list and the app: filter match (bundle ID, executable or window class,
// lower-cased); Name is for display; Path locates the app for its icon.
type Owner struct{ App, Name, Path string }

// ignoredApp reports whether o is on the ignore list. Empty owners never
// match.
func ignoredApp(o Owner, ignore []string) bool {
	app, name := strings.ToLower(o.App), strings.ToLower(o.Name)
	for _, i := range ignore {
		i = strings.ToLower(i)
		if i != "" && (i == app || i == name) {
			return true
		}
	}
	return false
}

// appendOwner appends o unless it is empty or its App is already in out.
func appendOwner(out []Owner, o Owner) []Owner {
	if o.App == "" {
		return out
	}
	for _, x := range out {
		if x.App == o.App {
			return out
		}
	}
	return append(out, o)
}
