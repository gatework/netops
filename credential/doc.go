// Package credential supplies device login credentials.
//
// A Provider looks credentials up per device. Env reads the process
// environment with the shared naming convention: NC_<PLATFORM>_USERNAME,
// _PASSWORD and _ENABLE_PASSWORD first, then NC_DEVICE_*. Static serves one
// fixed set, and Chain tries providers in order. Credentials hold their
// secrets as secret.Secret, so they never print or log.
//
// Reading .env files is the bootstrap package's job; this package only maps
// an environment to credentials.
package credential
