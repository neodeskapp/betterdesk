package store

// MigrationStepNames lists the steps Migrate runs on a database, in order.
func MigrationStepNames(dialect Dialect) []string {
	steps := migrationSteps(dialect)
	names := make([]string, len(steps))
	for i, st := range steps {
		names[i] = st.name
	}
	return names
}

// SplitStatements exposes the schema file splitter.
func SplitStatements(text string) []string { return splitStatements(text) }

// AdvisoryLockClass is the first key of the migration lock.
const AdvisoryLockClass = advisoryLockClass
