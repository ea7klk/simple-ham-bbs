from .config import PAGE_SIZE, t
from .storage import board_id_from_name, load_boards, now, save_boards
from .ui import (
    ask,
    banner,
    choice_menu,
    data_table,
    dialog_form,
    dialog_form_action,
    form_panel,
    line,
    paginated,
    pause,
)


def show_board_list(lang: str, boards: list) -> None:
    rows = [
        [
            str(idx),
            board.get("name", t(lang, "untitled")),
            str(len(board.get("messages", []))),
            board.get("description") or t(lang, "not_set"),
        ]
        for idx, board in enumerate(boards, 1)
    ]
    data_table(
        t(lang, "message_boards_title"),
        ["#", t(lang, "message_board"), "Msgs", t(lang, "board_description")],
        rows,
    )


def select_board(lang: str, boards: list, prompt_key: str = "select_board") -> dict | None:
    if not boards:
        line(t(lang, "no_boards"))
        pause(lang)
        return None
    options = [
        (
            str(index),
            f"{board.get('name', t(lang, 'untitled'))} "
            f"({len(board.get('messages', []))}) - "
            f"{board.get('description') or t(lang, 'not_set')}",
        )
        for index, board in enumerate(boards, 1)
    ]
    value = choice_menu(lang, t(lang, prompt_key), options, header=lambda: banner(lang))
    if value in {"q", "quit", "exit"}:
        return None
    if value.isdigit():
        index = int(value) - 1
        if 0 <= index < len(boards):
            return boards[index]
    line(t(lang, "invalid_choice"))
    pause(lang)
    return None


def message_label(lang: str, message: dict, number: int) -> str:
    return (
        f"{number}. {message.get('subject', t(lang, 'untitled'))} - "
        f"{message.get('from', 'UNKNOWN')} - {message.get('created', '')}"
    )


def show_message_detail(lang: str, board: dict, message: dict, number: int) -> None:
    banner(lang)
    rows = [
        [t(lang, "message_board"), board.get("name", t(lang, "untitled"))],
        [t(lang, "subject"), message.get("subject", t(lang, "untitled"))],
        [t(lang, "from"), message.get("from", "UNKNOWN")],
        [t(lang, "at"), message.get("created", "")],
        ["Text", message.get("body", "")],
    ]
    data_table(f"{t(lang, 'message_number')} {number}", ["Field", "Value"], rows)
    pause(lang)


def select_message(lang: str, board: dict, prompt_key: str = "select_message") -> int | None:
    messages = board.get("messages", [])
    if not messages:
        line(t(lang, "no_messages"))
        pause(lang)
        return None
    display_messages = messages[-100:]
    first_display_number = max(0, len(messages) - len(display_messages)) + 1
    options = [
        (str(first_display_number + index - 1), message_label(lang, message, first_display_number + index - 1))
        for index, message in enumerate(display_messages, 1)
    ]
    value = choice_menu(
        lang,
        t(lang, prompt_key),
        options,
        header=lambda: banner(lang),
    )
    if value in {"q", "quit", "exit"}:
        return None
    if value.isdigit():
        actual_index = int(value) - 1
        if 0 <= actual_index < len(messages):
            return actual_index
    line(t(lang, "invalid_message_number"))
    pause(lang)
    return None


def show_board_messages(lang: str, board: dict, include_numbers: bool = True) -> None:
    messages = board.get("messages", [])
    display_messages = messages[-100:]
    first_display_number = max(0, len(messages) - len(display_messages)) + 1

    def render(page_items: list, page: int, total: int) -> None:
        rows = []
        start_index = first_display_number + ((page - 1) * PAGE_SIZE)
        for idx, message in enumerate(page_items, 1):
            number = str(start_index + idx - 1) if include_numbers else ""
            rows.append(
                [
                    number,
                    message.get("subject", t(lang, "untitled")),
                    message.get("from", "UNKNOWN"),
                    message.get("created", ""),
                    message.get("body", ""),
                ]
            )
        if not rows:
            line(t(lang, "no_messages"))
            return
        data_table(
            f"{t(lang, 'message_board')}: {board.get('name', t(lang, 'untitled'))}",
            ["#", t(lang, "subject"), t(lang, "from"), t(lang, "at"), "Text"],
            rows,
        )

    paginated(lang, display_messages, t(lang, "message_boards_title"), render)


def show_messages(lang: str) -> None:
    banner(lang)
    boards_data = load_boards()
    board = select_board(lang, boards_data["boards"], "select_board")
    if not board:
        return
    while True:
        message_index = select_message(lang, board, "select_message")
        if message_index is None:
            return
        show_message_detail(lang, board, board.get("messages", [])[message_index], message_index + 1)


def post_message(callsign: str, lang: str) -> None:
    banner(lang)
    boards_data = load_boards()
    board = select_board(lang, boards_data["boards"], "select_board_post")
    if not board:
        return
    title = f"{t(lang, 'message_form_title')} - {board.get('name', t(lang, 'untitled'))}"
    result = dialog_form(
        lang,
        title,
        [
            {
                "name": "subject",
                "label": t(lang, "subject"),
                "value": "",
                "required": True,
                "limit": 80,
            },
            {
                "name": "body",
                "label": t(lang, "message_body"),
                "value": "",
                "required": True,
                "kind": "multiline",
                "limit": 4000,
                "height": 9,
            },
        ],
        allow_cancel=True,
    )
    if result is None:
        line(t(lang, "cancelled"))
        pause(lang)
        return
    subject = result["subject"].strip()
    body = result["body"].strip()

    board.setdefault("messages", []).append(
        {
            "from": callsign,
            "subject": subject,
            "body": body,
            "created": now(),
        }
    )
    board["messages"] = board["messages"][-500:]
    save_boards(boards_data)
    line(t(lang, "message_posted"))
    pause(lang)


def add_message_board(lang: str) -> None:
    boards_data = load_boards()
    title = t(lang, "board_form_title")
    result = dialog_form(
        lang,
        title,
        [
            {
                "name": "name",
                "label": t(lang, "board_name"),
                "value": "",
                "required": True,
                "limit": 60,
            },
            {
                "name": "description",
                "label": t(lang, "board_description"),
                "value": "",
                "required": False,
                "limit": 120,
            },
        ],
        allow_cancel=True,
    )
    if result is None:
        line(t(lang, "cancelled"))
        pause(lang)
        return
    name = result["name"].strip()
    board_id = board_id_from_name(name)
    existing_ids = {board.get("id") for board in boards_data["boards"]}
    if board_id in existing_ids:
        line(t(lang, "board_exists"))
        pause(lang)
        return
    description = result["description"].strip()
    form_panel(title, [(t(lang, "board_name"), name), (t(lang, "board_description"), description)])
    boards_data["boards"].append(
        {
            "id": board_id,
            "name": name,
            "description": description,
            "created": now(),
            "messages": [],
        }
    )
    save_boards(boards_data)
    line(t(lang, "board_created"))
    pause(lang)


def delete_message_board(lang: str) -> None:
    boards_data = load_boards()
    banner(lang)
    board = select_board(lang, boards_data["boards"], "select_board_delete")
    if not board:
        return
    if len(boards_data["boards"]) <= 1:
        line(t(lang, "cannot_delete_last_board"))
        pause(lang)
        return
    confirmation = ask(t(lang, "confirm_delete_board")).strip()
    if confirmation != "DELETE":
        line(t(lang, "delete_cancelled"))
        pause(lang)
        return
    boards_data["boards"] = [
        item for item in boards_data["boards"] if item.get("id") != board.get("id")
    ]
    save_boards(boards_data)
    line(t(lang, "board_deleted"))
    pause(lang)


def rename_message_board(lang: str) -> None:
    boards_data = load_boards()
    banner(lang)
    board = select_board(lang, boards_data["boards"], "select_board_rename")
    if not board:
        return
    title = t(lang, "board_rename_title")
    result = dialog_form(
        lang,
        title,
        [
            {
                "name": "name",
                "label": t(lang, "board_name"),
                "value": board.get("name", ""),
                "required": True,
                "limit": 60,
            },
        ],
        allow_cancel=True,
    )
    if result is None:
        line(t(lang, "cancelled"))
        pause(lang)
        return
    name = result["name"].strip()
    board_id = board_id_from_name(name)
    existing_ids = {
        item.get("id")
        for item in boards_data["boards"]
        if item.get("id") != board.get("id")
    }
    if board_id in existing_ids:
        line(t(lang, "board_exists"))
        pause(lang)
        return
    board["name"] = name
    board["id"] = board_id
    save_boards(boards_data)
    line(t(lang, "board_renamed"))
    pause(lang)


def edit_board_message(lang: str) -> None:
    boards_data = load_boards()
    banner(lang)
    board = select_board(lang, boards_data["boards"], "select_board_message_delete")
    if not board:
        return
    messages = board.get("messages", [])
    actual_index = select_message(lang, board, "select_message_delete")
    if actual_index is None:
        return
    message = messages[actual_index]
    title = (
        f"{t(lang, 'message_edit_title')} - "
        f"{board.get('name', t(lang, 'untitled'))} #{actual_index + 1}"
    )
    action, result = dialog_form_action(
        lang,
        title,
        [
            {
                "name": "subject",
                "label": t(lang, "subject"),
                "value": message.get("subject", ""),
                "required": True,
                "limit": 80,
            },
            {
                "name": "body",
                "label": t(lang, "message_body"),
                "value": message.get("body", ""),
                "required": True,
                "kind": "multiline",
                "limit": 4000,
                "height": 9,
            },
        ],
        allow_cancel=True,
        buttons=["cancel", "save", "delete"],
    )
    if action == "cancel":
        line(t(lang, "cancelled"))
        pause(lang)
        return
    if action == "delete":
        messages.pop(actual_index)
        save_boards(boards_data)
        line(t(lang, "message_deleted"))
        pause(lang)
        return
    if not result:
        line(t(lang, "cancelled"))
        pause(lang)
        return
    message["subject"] = result["subject"].strip()
    message["body"] = result["body"].strip()
    message["edited"] = now()
    save_boards(boards_data)
    line(t(lang, "message_updated"))
    pause(lang)


def delete_board_message(lang: str) -> None:
    edit_board_message(lang)
