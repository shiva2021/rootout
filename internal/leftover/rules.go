package leftover

import "github.com/shiva2021/purge/internal/fsx"

// matchKind names one way a directory entry can belong to an app. The engine
// in leftover.go (scan.match) implements each one.
type matchKind int

const (
	matchID            matchKind = iota // basename == bundle id                      -> "bundle_id"
	matchIDPrefix                       // basename has prefix "<id>."               -> "bundle_prefix"
	matchPlist                          // "<id>.plist" or "<id>.<x>.plist"          -> "bundle_id" / "bundle_prefix"
	matchTeam                           // "<team>.<id>", "<team>.group.<id>[.x]"    -> "team_bundle"; skipped without TeamID
	matchGroup                          // "group.<id>"                              -> "team_bundle"
	matchName                           // basename == app name, requires NameOK     -> "name"
	matchDotName                        // basename == ".<name>", requires NameOK    -> "name"
	matchSavedState                     // "<id>.savedState"                         -> "bundle_id"
	matchBinaryCookies                  // "<id>.binarycookies"                      -> "bundle_id"
	matchLaunchd                        // launchd plist: "<id>.*" or program inside the .app
	matchHelperLabel                    // basename == Label of an already matched LaunchDaemon
	matchCrashReport                    // "<Name>-*.ips|crash|diag" (raw name)      -> "name"
)

// rule is one row of the leftover table: a directory to list, the Kind its
// matches get, the matchers tried in order, and an optional opt-in gate.
type rule struct {
	dir      func(fsx.Env) string
	kind     Kind
	matchers []matchKind
	when     func(Options) bool // nil means always
}

func lib(parts ...string) func(fsx.Env) string {
	return func(env fsx.Env) string { return env.Library(parts...) }
}

func sys(parts ...string) func(fsx.Env) string {
	return func(env fsx.Env) string { return env.Sys(parts...) }
}

func home(parts ...string) func(fsx.Env) string {
	return func(env fsx.Env) string { return env.User(parts...) }
}

func crashReports(o Options) bool { return o.CrashReports }
func dotfiles(o Options) bool     { return o.Dotfiles }

// rules is the leftover table from docs/kb/safety.md. Name matching (N) never
// appears for Group Containers and, under /Library, only for Application
// Support and Logs. The LaunchDaemons row must precede PrivilegedHelperTools:
// the helper row consumes the daemon labels collected by the former.
var rules = []rule{
	{dir: lib("Preferences"), kind: Preferences, matchers: []matchKind{matchPlist}},
	{dir: lib("Preferences", "ByHost"), kind: Preferences, matchers: []matchKind{matchPlist}},
	{dir: sys("Library", "Preferences"), kind: Preferences, matchers: []matchKind{matchPlist}},

	{dir: lib("Caches"), kind: Cache, matchers: []matchKind{matchID, matchIDPrefix, matchName}},
	{dir: lib("Logs"), kind: Log, matchers: []matchKind{matchID, matchIDPrefix, matchName}},
	{dir: lib("Application Support"), kind: AppSupport, matchers: []matchKind{matchID, matchIDPrefix, matchName}},
	{dir: sys("Library", "Caches"), kind: Cache, matchers: []matchKind{matchID}},
	{dir: sys("Library", "Application Support"), kind: AppSupport, matchers: []matchKind{matchID, matchName}},
	{dir: sys("Library", "Logs"), kind: Log, matchers: []matchKind{matchID, matchName}},

	{dir: lib("Containers"), kind: Container, matchers: []matchKind{matchID, matchTeam}},
	{dir: lib("Group Containers"), kind: GroupContainer, matchers: []matchKind{matchTeam, matchGroup}},

	{dir: lib("Saved Application State"), kind: SavedState, matchers: []matchKind{matchSavedState}},
	{dir: lib("HTTPStorages"), kind: HTTPStorage, matchers: []matchKind{matchID, matchBinaryCookies}},
	{dir: lib("Cookies"), kind: Cookies, matchers: []matchKind{matchBinaryCookies}},
	{dir: lib("WebKit"), kind: WebKit, matchers: []matchKind{matchID}},
	{dir: lib("Application Scripts"), kind: AppScripts, matchers: []matchKind{matchID}},

	{dir: lib("LaunchAgents"), kind: LaunchAgent, matchers: []matchKind{matchLaunchd}},
	{dir: sys("Library", "LaunchAgents"), kind: LaunchAgent, matchers: []matchKind{matchLaunchd}},
	{dir: sys("Library", "LaunchDaemons"), kind: LaunchDaemon, matchers: []matchKind{matchLaunchd}},
	{dir: sys("Library", "PrivilegedHelperTools"), kind: PrivilegedHelper, matchers: []matchKind{matchIDPrefix, matchHelperLabel}},

	{dir: lib("Logs", "DiagnosticReports"), kind: CrashReport, matchers: []matchKind{matchCrashReport}, when: crashReports},

	{dir: home(".config"), kind: Dotfile, matchers: []matchKind{matchName}, when: dotfiles},
	{dir: home(), kind: Dotfile, matchers: []matchKind{matchDotName}, when: dotfiles},
}
