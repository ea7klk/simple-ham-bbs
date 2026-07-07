from .aprs import (
    APRS_MESSAGE_LIMIT,
    add_sent_message,
    normalize_aprs_callsign,
    send_aprs_message,
    trim_sent_messages,
    valid_aprs_callsign,
)
from .auth import collect_profile_form, normalize_user_callsigns
from .config import BBS_SYSOP, LANGUAGES, t
from .storage import BULLETINS_FILE, USERS_FILE, load_json, save_json, now
from .ui import (
    banner,
    choice_menu,
    data_table,
    dialog_form,
    dialog_form_action,
    line,
    paginated,
    pause,
    select_menu,
)


def show_bulletins(lang: str) -> None:
    items = load_json(BULLETINS_FILE, [])
    if not items:
        banner(lang)
        line(t(lang, "no_bulletins"))
        pause(lang)
        return

    options = [
        (str(index), f"{item.get('title', t(lang, 'untitled'))} - {item.get('updated', '')}")
        for index, item in enumerate(items, 1)
    ]
    choice = choice_menu(lang, t(lang, "menu_bulletins"), options, header=lambda: banner(lang))
    if choice in {"q", "quit", "exit"}:
        return
    if not choice.isdigit() or not (1 <= int(choice) <= len(items)):
        line(t(lang, "invalid_choice"))
        pause(lang)
        return
    item = items[int(choice) - 1]
    banner(lang)
    data_table(
        item.get("title", t(lang, "untitled")),
        [t(lang, "at"), "Text"],
        [[item.get("updated", ""), item.get("body", "")]],
    )
    pause(lang)


def show_bulletin_index(lang: str) -> None:
    banner(lang)
    items = load_json(BULLETINS_FILE, [])

    def render(page_items: list, page: int, total: int) -> None:
        rows = [
            [item.get("title", t(lang, "untitled")), item.get("updated", ""), item.get("body", "")]
            for item in page_items
        ]
        data_table(t(lang, "menu_bulletins"), ["Title", t(lang, "at"), "Text"], rows)

    paginated(lang, items, t(lang, "menu_bulletins"), render)
    pause(lang)


def publish_bulletin(callsign: str, lang: str) -> None:
    result = dialog_form(
        lang,
        t(lang, "bulletin_form_title"),
        [
            {
                "name": "title",
                "label": t(lang, "bulletin_title"),
                "value": "",
                "required": True,
                "limit": 100,
            },
            {
                "name": "body",
                "label": t(lang, "bulletin_body"),
                "value": "",
                "required": True,
                "kind": "multiline",
                "height": 9,
                "limit": 4000,
            },
        ],
        allow_cancel=True,
    )
    if result is None:
        line(t(lang, "cancelled"))
        pause(lang)
        return
    bulletins = load_json(BULLETINS_FILE, [])
    if not isinstance(bulletins, list):
        bulletins = []
    bulletins.append(
        {
            "title": result["title"].strip(),
            "body": result["body"].strip(),
            "updated": now(),
            "from": callsign,
        }
    )
    save_json(BULLETINS_FILE, bulletins)
    line(t(lang, "bulletin_published"))
    pause(lang)


def station_directory(lang: str) -> None:
    banner(lang)
    users = load_json(USERS_FILE, {})
    active_users = [
        (callsign, profile)
        for callsign, profile in sorted(users.items())
        if not profile.get("disabled")
    ]

    def render(page_items: list, page: int, total: int) -> None:
        rows = []
        for callsign, profile in page_items:
            rows.append(
                [
                    callsign,
                    profile.get("last_seen", ""),
                    profile.get("full_name") or t(lang, "not_set"),
                    profile.get("maidenhead") or t(lang, "not_set"),
                    LANGUAGES.get(profile.get("language", ""), t(lang, "not_set")),
                ]
            )
        data_table(
            t(lang, "menu_directory"),
            ["Callsign", "Last seen", "Name", "Locator", "Language"],
            rows,
        )

    paginated(lang, active_users, t(lang, "menu_directory"), render)
    pause(lang)


def change_profile(callsign: str, profile: dict, lang: str) -> tuple[dict, str]:
    banner(lang)
    users = load_json(USERS_FILE, {})
    profile = users.setdefault(callsign, profile)
    line(t(lang, "keep_blank"))

    updated_profile = collect_profile_form(
        callsign,
        profile,
        lang,
        required=False,
        title_key="profile_form_title",
        include_password_change=True,
    )
    if updated_profile is None:
        line(t(lang, "cancelled"))
        pause(lang)
        return profile, lang
    profile = updated_profile

    profile["last_seen"] = now()
    users[callsign] = profile
    save_json(USERS_FILE, users)
    line(t(profile["language"], "profile_updated"))
    pause(profile["language"])
    return profile, profile["language"]


def radio_resources(lang: str) -> None:
    banner(lang)
    rows = [
        [t(lang, "hamnet_address")],
        [t(lang, "local_ssh")],
        [t(lang, "hamnet_ssh")],
        [t(lang, "ideas")],
        ["- Local net schedules"],
        ["- Repeater status"],
        ["- Packet, APRS, and mesh experiments"],
        ["- Field-day planning notes"],
    ]
    data_table(t(lang, "resources_title"), [t(lang, "resources_title")], rows)
    pause(lang)


def aprs_header(callsign: str, profile: dict, lang: str) -> None:
    banner(lang)
    rows = [
        [t(lang, "aprs_status"), "true" if profile.get("enable_aprs") else "false"],
        [t(lang, "aprs_ssid_info")],
        [t(lang, "aprs_not_enabled")],
        [t(lang, "planned_extensions")],
        ["- APRS-IS server, callsign, and passcode settings from environment"],
        ["- Inbox/outbox persisted under /var/lib/bbs/aprs"],
        ["- Dry-run mode for local testing"],
        ["- Menu actions for sending short APRS messages"],
    ]
    data_table(t(lang, "aprs_title"), [t(lang, "aprs_title")], rows)


def edit_aprs_enabled(callsign: str, profile: dict, users: dict, lang: str) -> dict:
    result = dialog_form(
        lang,
        t(lang, "aprs_enable_title"),
        [
            {
                "name": "enable_aprs",
                "label": t(lang, "enable_aprs"),
                "value": "true" if profile.get("enable_aprs") else "false",
                "required": False,
                "kind": "choice",
                "choices": [("false", "false"), ("true", "true")],
                "validator": lambda value: value in {"true", "false"},
                "invalid_key": "invalid_choice",
            },
        ],
        allow_cancel=True,
    )
    if result is None:
        line(t(lang, "cancelled"))
        return profile
    profile["enable_aprs"] = str(result.get("enable_aprs", "false")) == "true"
    profile["last_seen"] = now()
    users[callsign] = profile
    save_json(USERS_FILE, users)
    line(t(lang, "aprs_status_updated"))
    return profile


def aprs_placeholder_submenu(lang: str, title_key: str, message_key: str) -> None:
    banner(lang)
    rows = [
        [t(lang, message_key)],
        [t(lang, "aprs_ssid_info")],
        [t(lang, "aprs_not_enabled")],
    ]
    data_table(t(lang, title_key), [t(lang, title_key)], rows)


def show_sent_aprs_messages(lang: str, messages: list[dict]) -> None:
    rows = [
        [
            message.get("at", ""),
            message.get("to", ""),
            message.get("text", ""),
            message.get("status", ""),
        ]
        for message in reversed(messages[-10:])
    ]
    if not rows:
        rows = [[t(lang, "aprs_no_sent_messages"), "", "", ""]]
    data_table(
        t(lang, "aprs_latest_sent"),
        [t(lang, "at"), t(lang, "aprs_destination_callsign"), t(lang, "aprs_text"), t(lang, "status")],
        rows,
    )


def send_aprs_message_screen(callsign: str, profile: dict, lang: str) -> None:
    sent_messages = trim_sent_messages(callsign)
    while True:
        banner(lang)
        rows = [
            [t(lang, "aprs_status"), "true" if profile.get("enable_aprs") else "false"],
            [t(lang, "aprs_ssid_info")],
        ]
        data_table(t(lang, "aprs_send_message"), [t(lang, "aprs_send_message")], rows)
        show_sent_aprs_messages(lang, sent_messages)
        if not profile.get("enable_aprs"):
            line(t(lang, "aprs_enable_required"))
            return
        action, result = dialog_form_action(
            lang,
            t(lang, "aprs_send_message"),
            [
                {
                    "name": "destination",
                    "label": t(lang, "aprs_destination_callsign"),
                    "value": "",
                    "required": True,
                    "limit": 9,
                    "normalizer": normalize_aprs_callsign,
                    "validator": valid_aprs_callsign,
                    "invalid_key": "aprs_invalid_destination",
                },
                {
                    "name": "text",
                    "label": t(lang, "aprs_text"),
                    "value": "",
                    "required": True,
                    "kind": "multiline",
                    "height": 5,
                    "limit": APRS_MESSAGE_LIMIT,
                },
            ],
            allow_cancel=True,
            buttons=["send", "cancel"],
        )
        if action == "cancel" or not result:
            return
        destination = normalize_aprs_callsign(result["destination"])
        text = str(result["text"]).strip()
        ok, detail = send_aprs_message(callsign, destination, text)
        if ok:
            status = t(lang, "aprs_sent_status_sent")
            add_sent_message(callsign, destination, text, status)
            line(t(lang, "aprs_send_success"))
        else:
            if detail == "invalid_source":
                line(t(lang, "aprs_invalid_source"))
            elif detail == "invalid_destination":
                line(t(lang, "aprs_invalid_destination"))
            else:
                line(f"{t(lang, 'aprs_send_failed')}: {detail}")
            status = t(lang, "aprs_sent_status_failed")
            add_sent_message(callsign, destination, text, status)
        sent_messages = trim_sent_messages(callsign)


def aprs_menu(callsign: str, profile: dict, lang: str) -> dict:
    users = load_json(USERS_FILE, {})
    users, users_changed = normalize_user_callsigns(users)
    profile = users.setdefault(callsign, profile)
    if users_changed:
        save_json(USERS_FILE, users)

    while True:
        choice = select_menu(
            lang,
            t(lang, "menu_aprs"),
            [
                ("1", t(lang, "aprs_set_enabled")),
                ("2", t(lang, "aprs_received_messages")),
                ("3", t(lang, "aprs_send_message")),
                ("q", t(lang, "menu_quit")),
            ],
            header=lambda: aprs_header(callsign, profile, lang),
            allow_quit=False,
        )
        if choice == "1":
            profile = edit_aprs_enabled(callsign, profile, users, lang)
        elif choice == "2":
            aprs_placeholder_submenu(lang, "aprs_received_messages", "aprs_received_placeholder")
        elif choice == "3":
            send_aprs_message_screen(callsign, profile, lang)
        elif choice in {"q", "quit", "exit"}:
            return profile


def about(lang: str) -> None:
    banner(lang)
    rows = [
        [t(lang, "about_transport")],
        [t(lang, "about_hamnet")],
        [t(lang, "about_storage")],
        [t(lang, "about_graphics")],
        [t(lang, "about_note")],
    ]
    data_table(t(lang, "about_title"), [t(lang, "about_title")], rows)
    pause(lang)
