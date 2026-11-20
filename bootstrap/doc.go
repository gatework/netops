// Package bootstrap gathers what a command-line tool needs before it can
// work: its configuration file, credentials environment, paths, logger and
// a file-descriptor budget for parallel sessions.
//
// Discovery is explicit and predictable: $<APP>_CONFIG, then ./<app>.yml,
// then $XDG_CONFIG_HOME/<app>/<app>.yml, then ~/.config/<app>/<app>.yml.
// YAML is decoded strictly, so a misspelled key is an error. Credentials
// come from the process environment overlaid on a .env file next to the
// configuration, which must be private (mode 0600).
package bootstrap
