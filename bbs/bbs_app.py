#!/usr/bin/env python3
from bbs_lib.auth import authenticate_user, is_sysop
from bbs_lib.boards import post_message, show_messages
from bbs_lib.config import BBS_SYSOP, t
from bbs_lib.screens import (
    about,
    aprs_menu,
    change_profile,
    radio_resources,
    show_bulletins,
    station_directory,
)
from bbs_lib.storage import seed_data
from bbs_lib.sysop import sysop_administration
from bbs_lib.ui import banner, line, select_menu


def menu(callsign: str, profile: dict, lang: str) -> str:
    def header() -> None:
        banner(lang)
        role = f" [{t(lang, 'sysop_role')}]" if is_sysop(callsign, profile) else ""
        line(
            f"{t(lang, 'logged_in_as')} {callsign}{role}    "
            f"{t(lang, 'sysop')}: {BBS_SYSOP}"
        )

    rows = [
        ("1", t(lang, "menu_bulletins")),
        ("2", t(lang, "menu_messages")),
        ("3", t(lang, "menu_post")),
        ("4", t(lang, "menu_directory")),
        ("5", t(lang, "menu_profile")),
        ("6", t(lang, "menu_resources")),
        ("7", t(lang, "menu_aprs")),
        ("8", t(lang, "menu_about")),
    ]
    if is_sysop(callsign, profile):
        rows.append(("9", t(lang, "menu_sysop")))
    rows.append(("q", t(lang, "menu_quit")))
    return select_menu(lang, t(lang, "select"), rows, header=header, allow_quit=False)


def main() -> int:
    seed_data()
    callsign, profile = authenticate_user()
    lang = profile.get("language", "en")

    while True:
        choice = menu(callsign, profile, lang)
        if choice == "1":
            show_bulletins(lang)
        elif choice == "2":
            show_messages(lang)
        elif choice == "3":
            post_message(callsign, lang)
        elif choice == "4":
            station_directory(lang)
        elif choice == "5":
            profile, lang = change_profile(callsign, profile, lang)
        elif choice == "6":
            radio_resources(lang)
        elif choice == "7":
            profile = aprs_menu(callsign, profile, lang)
        elif choice == "8":
            about(lang)
        elif choice == "9" and is_sysop(callsign, profile):
            sysop_administration(callsign, lang)
        elif choice in {"q", "quit", "exit"}:
            banner(lang)
            line(t(lang, "goodbye"))
            return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        line(f"\n{t('en', 'disconnected')}")
        raise SystemExit(0)
