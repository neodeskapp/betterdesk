package config

// Where the enrollment mode in force comes from (ResolveEnrollmentMode).
const (
	// EnrollmentSourceEnvironment: ENROLLMENT_MODE_ENV_OVERRIDE=Y forces
	// ENROLLMENT_MODE at every start; the console can't change it.
	EnrollmentSourceEnvironment = "environment"
	// EnrollmentSourceDatabase: the mode saved in the console (or stored
	// from ENROLLMENT_MODE as the first default).
	EnrollmentSourceDatabase = "database"
	// EnrollmentSourceConfiguration: nothing is saved, the configured mode
	// (ENROLLMENT_MODE at the first start, else the built-in default)
	// applies.
	EnrollmentSourceConfiguration = "configuration"
)

// ResolveEnrollmentMode is the enrollment mode in force and where it comes
// from: the configured mode when the environment forces it, else the
// stored mode when it is a valid one, else the configured mode. The start
// and the API's read of the mode both use it.
func ResolveEnrollmentMode(configuredMode, storedMode string, envOverride bool) (mode, source string) {
	if envOverride {
		return configuredMode, EnrollmentSourceEnvironment
	}
	switch storedMode {
	case EnrollmentModeOpen, EnrollmentModeManaged, EnrollmentModeLocked:
		return storedMode, EnrollmentSourceDatabase
	default:
		return configuredMode, EnrollmentSourceConfiguration
	}
}
