package releases

// RootFlagKinds exposes ParseArgs' flag tables to the external test package.
func RootFlagKinds() (map[string]bool, map[byte]bool) {
	return rootLongFlags, rootShortFlags
}
