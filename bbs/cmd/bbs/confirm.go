package main

func (a *app) confirmDelete(lang, title string) bool {
	action, values, ok := a.runForm(lang, title, []formField{
		{
			name:  "confirm_delete",
			label: a.t(lang, "confirm_delete_field"),
			kind:  fieldChoice,
			value: "no",
			choices: []option{
				{"no", a.t(lang, "no_label")},
				{"yes", a.t(lang, "yes_label")},
			},
		},
	}, []string{"no", "yes"})
	return ok && action == "yes" && values["confirm_delete"] == "yes"
}
