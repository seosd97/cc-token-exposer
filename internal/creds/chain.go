package creds

func Default() *Resolver {
	return NewResolver(
		&FileSource{},
		newKeychainSource(),
	)
}
