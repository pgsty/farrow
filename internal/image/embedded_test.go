package image

func embeddedEntry(alias, arch string) (Entry, error) {
	return EmbeddedCatalog().Entry(alias, arch)
}
