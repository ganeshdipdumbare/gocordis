package loader

// FlatEntry is one entry in a flattened tree.
type FlatEntry struct {
	Options  EntryOptions
	Parent   string
	Position int
}

// Flatten indexes every entry by id. Nested group members are included, with
// Parent set to their group id ("" for the root).
func Flatten(entries []EntryOptions) map[string]FlatEntry {
	result := map[string]FlatEntry{}
	var walk func(list []EntryOptions, parent string)
	walk = func(list []EntryOptions, parent string) {
		for index, options := range list {
			options.Entries = nil
			result[options.ID] = FlatEntry{Options: options, Parent: parent, Position: index}
			walk(list[index].Entries, options.ID)
		}
	}
	walk(entries, "")
	return result
}

// UpdateInTree replaces one entry by id.
func UpdateInTree(entries []EntryOptions, id string, fn func(*EntryOptions)) bool {
	return updateInTree(entries, id, fn)
}

// RemoveFromTree deletes one entry by id.
func RemoveFromTree(entries []EntryOptions, id string) ([]EntryOptions, bool) {
	return removeFromTree(entries, id)
}

// InsertIntoTree inserts an entry under a group id, or at the root for "".
func InsertIntoTree(entries []EntryOptions, parent string, options EntryOptions) ([]EntryOptions, bool) {
	return insertIntoTree(entries, parent, options)
}
