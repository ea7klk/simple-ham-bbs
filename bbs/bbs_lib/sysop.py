from .auth import (
    apply_configured_sysops,
    is_sysop,
    normalize_user_callsigns,
    would_remove_last_sysop,
)
from .boards import (
    add_message_board,
    delete_message_board,
    edit_board_message,
    rename_message_board,
)
from .config import t
from .screens import publish_bulletin
from .storage import USERS_FILE, load_json, save_json
from .ui import ask, banner, choice_menu, data_table, line, pause, select_menu


def show_user_admin_list(lang: str, users: dict) -> None:
    rows = []
    for callsign, profile in sorted(users.items()):
        status = t(lang, "disabled") if profile.get("disabled") else t(lang, "enabled")
        role = t(lang, "sysop_role") if is_sysop(callsign, profile) else t(lang, "user_role")
        name = profile.get("full_name") or t(lang, "not_set")
        email = profile.get("email") or t(lang, "not_set")
        rows.append([callsign, status, role, name, email])
    data_table(
        t(lang, "sysop_list_users"),
        ["Callsign", "Status", "Role", "Name", "Email"],
        rows,
    )


def ask_existing_callsign(lang: str, users: dict, current_callsign: str) -> str:
    options = []
    for index, (callsign, profile) in enumerate(sorted(users.items()), 1):
        if callsign == current_callsign:
            continue
        status = t(lang, "disabled") if profile.get("disabled") else t(lang, "enabled")
        role = t(lang, "sysop_role") if is_sysop(callsign, profile) else t(lang, "user_role")
        name = profile.get("full_name") or t(lang, "not_set")
        options.append((str(index), f"{callsign} - {status} - {role} - {name}"))
    if not options:
        line(t(lang, "user_not_found"))
        return ""
    choice = choice_menu(
        lang,
        t(lang, "target_callsign"),
        options,
        header=lambda: banner(lang),
    )
    if choice in {"q", "quit", "exit"}:
        return ""
    for value, label in options:
        if choice == value:
            return label.split(" - ", 1)[0]
    line(t(lang, "user_not_found"))
    return ""


def toggle_sysop_user(current_callsign: str, lang: str, users: dict) -> None:
    from .config import BBS_SYSOPS

    callsign = ask_existing_callsign(lang, users, current_callsign)
    if not callsign:
        pause(lang)
        return
    profile = users[callsign]
    if is_sysop(callsign, profile):
        if callsign in BBS_SYSOPS:
            line(t(lang, "cannot_demote_configured"))
        elif would_remove_last_sysop(users, callsign):
            line(t(lang, "cannot_remove_last_sysop"))
        else:
            profile["is_sysop"] = False
            line(t(lang, "user_updated"))
    else:
        profile["is_sysop"] = True
        profile["disabled"] = False
        line(t(lang, "user_updated"))
    save_json(USERS_FILE, users)
    pause(lang)


def toggle_disabled_user(current_callsign: str, lang: str, users: dict) -> None:
    callsign = ask_existing_callsign(lang, users, current_callsign)
    if not callsign:
        pause(lang)
        return
    profile = users[callsign]
    if not profile.get("disabled") and would_remove_last_sysop(users, callsign):
        line(t(lang, "cannot_remove_last_sysop"))
    else:
        profile["disabled"] = not profile.get("disabled")
        line(t(lang, "user_updated"))
    save_json(USERS_FILE, users)
    pause(lang)


def delete_user(current_callsign: str, lang: str, users: dict) -> None:
    callsign = ask_existing_callsign(lang, users, current_callsign)
    if not callsign:
        pause(lang)
        return
    if would_remove_last_sysop(users, callsign):
        line(t(lang, "cannot_remove_last_sysop"))
        pause(lang)
        return
    confirmation = ask(t(lang, "confirm_delete")).strip()
    if confirmation != "DELETE":
        line(t(lang, "delete_cancelled"))
        pause(lang)
        return
    users.pop(callsign, None)
    save_json(USERS_FILE, users)
    line(t(lang, "user_deleted"))
    pause(lang)


def sysop_administration(current_callsign: str, lang: str) -> None:
    while True:
        users = load_json(USERS_FILE, {})
        users, users_changed = normalize_user_callsigns(users)
        if apply_configured_sysops(users):
            users_changed = True
        if users_changed:
            save_json(USERS_FILE, users)
        choice = ask_sysop_menu(lang)
        if choice == "1":
            banner(lang)
            show_user_admin_list(lang, users)
            pause(lang)
        elif choice == "2":
            banner(lang)
            show_user_admin_list(lang, users)
            toggle_sysop_user(current_callsign, lang, users)
        elif choice == "3":
            banner(lang)
            show_user_admin_list(lang, users)
            toggle_disabled_user(current_callsign, lang, users)
        elif choice == "4":
            banner(lang)
            show_user_admin_list(lang, users)
            delete_user(current_callsign, lang, users)
        elif choice == "5":
            publish_bulletin(current_callsign, lang)
        elif choice == "6":
            add_message_board(lang)
        elif choice == "7":
            delete_message_board(lang)
        elif choice == "8":
            rename_message_board(lang)
        elif choice == "9":
            edit_board_message(lang)
        elif choice in {"q", "quit", "exit"}:
            return


def ask_sysop_menu(lang: str) -> str:
    rows = [
        ("1", t(lang, "sysop_list_users")),
        ("2", t(lang, "sysop_toggle_sysop")),
        ("3", t(lang, "sysop_toggle_disabled")),
        ("4", t(lang, "sysop_delete_user")),
        ("5", t(lang, "sysop_publish_bulletin")),
        ("6", t(lang, "sysop_add_board")),
        ("7", t(lang, "sysop_delete_board")),
        ("8", t(lang, "sysop_rename_board")),
        ("9", t(lang, "sysop_delete_message")),
        ("q", t(lang, "menu_quit")),
    ]
    return select_menu(
        lang,
        t(lang, "sysop_menu_title"),
        rows,
        header=lambda: banner(lang),
        allow_quit=False,
    )
