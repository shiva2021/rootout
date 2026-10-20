package safety

import "github.com/shiva2021/purge/internal/fsx"

// allowedPrefixes are the only roots deletions may live under. Dotfile
// prefixes (~/.config, ~/.<x>) are handled separately because they are opt-in.
func allowedPrefixes(env fsx.Env) []string {
	return []string{
		env.Sys("Applications"),
		env.User("Applications"),
		env.Library(),
		env.Sys("Library"),
	}
}

// protectedPrefixes are never deleted, nor is anything under them. They are
// checked before the allowlist so a protected subtree of an allowed prefix
// (e.g. /Library/Apple) is still refused.
func protectedPrefixes(env fsx.Env) []string {
	return []string{
		env.Sys("System"),
		env.Sys("usr"),
		env.Sys("bin"),
		env.Sys("sbin"),
		env.Sys("private"),
		env.Sys("Library", "Apple"),
		env.Sys("Library", "Developer", "CommandLineTools"),
		env.Library("Keychains"),
		env.Library("Mail"),
		env.Library("Messages"),
		env.Library("Photos"),
		env.Library("Safari"),
		env.User(".ssh"),
		env.User(".gnupg"),
		env.User(".aws"),
		env.User(".Trash"),
	}
}

// protectedExact are directories that may not be deleted as a whole, although
// individual entries inside them may be.
func protectedExact(env fsx.Env) []string {
	return []string{
		env.Sys("Applications", "Utilities"),
	}
}
