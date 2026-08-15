module strawb.love/game

go 1.26

require (
	game v0.0.0
	github.com/traefik/yaegi v0.16.1
)

// The player-facing sentry7.go imports the bare path "game" — that exact text
// is what she reads in the in-game editor, so it cannot say strawb.love/anything.
// Natively the import resolves to ./gamefacade via this replace; inside yaegi it
// resolves to the patch package's interp.Exports key "game/game". Same four
// names either way.
replace game => ./gamefacade
