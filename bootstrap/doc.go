// Package bootstrap gathers what a command-line tool needs before it can
// work: its configuration file, credentials environment, paths, logger and
// a file-descriptor budget for parallel sessions.
//
// Every gatework tool shares one layout. Configuration is discovered as
// $<APP>_CONFIG, then ./<app>.yml, then ~/.gatework/<app>.yml. Data the tool
// keeps between runs (caches, snapshots, journals, recordings) lives under
// ~/.local/share/gatework/<app> (independent of $XDG_DATA_HOME).
// YAML is decoded strictly, so a misspelled key is an error. Credentials
// come from the process environment overlaid on a .env file next to the
// configuration, which must be private (mode 0600); with the default
// layout that is ~/.gatework/.env, shared by every tool.
package bootstrap
