package state

func (s Store) WriteTransaction(value Transaction) error {
	if err := validateTransaction(value); err != nil {
		return err
	}
	if _, err := s.EnsureNodeDir(value.Node); err != nil {
		return err
	}
	path, err := s.nodePath(value.Node, "transaction.json")
	if err != nil {
		return err
	}
	return writeJSON(path, value)
}
