package main

func (a *app) confirmDelete(lang, title string) bool {
	if a.confirmDeleteHook != nil {
		return a.confirmDeleteHook(lang, title)
	}
	action := a.showInfoActions(lang, title, [][]string{{a.t(lang, "confirm_delete_field")}}, []option{
		{"n", a.t(lang, "no_label")},
		{"y", a.t(lang, "yes_label")},
	})
	return action == "y"
}
