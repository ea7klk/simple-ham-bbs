import getpass
import os
import sys
import termios
import tty
from typing import Callable, Iterable

from rich import box
from rich.align import Align
from rich.console import Console, Group
from rich.panel import Panel
from rich.prompt import Prompt
from rich.table import Table
from rich.text import Text

from .config import (
    BBS_LOCATION,
    BBS_NAME,
    BBS_WELCOME_TOPIC,
    LANGUAGES,
    PAGE_SIZE,
    t,
)


console = Console(
    force_terminal=os.environ.get("BBS_FORCE_RICH", "1") != "0",
    color_system="standard",
    soft_wrap=True,
)


def clear() -> None:
    console.clear()


def line(text: str = "") -> None:
    console.print(text, markup=False)


def banner(lang: str = "en", show_login_info: bool = False) -> None:
    clear()
    title = Text(BBS_NAME, style="bold cyan")
    subtitle = Text(f"{BBS_LOCATION} - {BBS_WELCOME_TOPIC}", style="green")
    console.print(
        Panel(
            Align.center(Group(title, subtitle)),
            border_style="cyan",
            box=box.DOUBLE,
        )
    )
    if show_login_info:
        for code in ("en", "es", "fr", "de"):
            body = "\n".join(f"- {item}" for item in t(code, "login_info"))
            console.print(
                Panel(
                    body,
                    title=LANGUAGES[code],
                    border_style="blue",
                    box=box.ROUNDED,
                )
            )


def ask(prompt: str) -> str:
    label = prompt.strip()
    if label.endswith(":"):
        label = label[:-1].rstrip()
    return Prompt.ask(label, console=console).strip()


def ask_secret(prompt: str) -> str:
    if sys.stdin.isatty():
        return getpass.getpass(f"{prompt}: ").strip()
    return ask(prompt)


def pause(lang: str) -> None:
    return None


def confirm_delete(prompt: str) -> bool:
    return ask(prompt) == "DELETE"


def info(message: str, style: str = "green") -> None:
    console.print(Panel(message, border_style=style, box=box.ROUNDED))


def is_interactive() -> bool:
    return sys.stdin.isatty() and sys.stdout.isatty()


def read_key() -> str:
    fd = sys.stdin.fileno()
    old_settings = termios.tcgetattr(fd)
    try:
        tty.setraw(fd)
        char = sys.stdin.read(1)
        if char == "\x1b":
            char += sys.stdin.read(2)
        return char
    finally:
        termios.tcsetattr(fd, termios.TCSADRAIN, old_settings)


def menu_hint(lang: str) -> str:
    return (
        f"{t(lang, 'menu_hint_arrows')}  "
        f"{t(lang, 'menu_hint_enter')}  "
        f"{t(lang, 'menu_hint_quit')}"
    )


def select_menu(
    lang: str,
    title: str,
    options: list[tuple[str, str]],
    header: Callable[[], None] | None = None,
    prompt_key: str = "select",
    allow_quit: bool = True,
    default_value: str = "",
) -> str:
    if allow_quit and not any(value.lower() == "q" for value, _ in options):
        options = [*options, ("q", t(lang, "menu_quit"))]

    if not is_interactive():
        data_table(title, ["#", t(lang, "select")], [[value, label] for value, label in options])
        return ask(t(lang, prompt_key)).lower() or default_value.lower()

    selected = 0
    if default_value:
        for index, (value, _) in enumerate(options):
            if value.lower() == default_value.lower():
                selected = index
                break
    while True:
        if header:
            header()
        else:
            clear()

        window_size = max(PAGE_SIZE, 8)
        if len(options) > window_size:
            start = max(0, min(selected - window_size // 2, len(options) - window_size))
            visible_options = options[start : start + window_size]
        else:
            start = 0
            visible_options = options

        table = Table.grid(padding=(0, 2))
        table.add_column(no_wrap=True)
        table.add_column()
        for offset, (value, label) in enumerate(visible_options):
            index = start + offset
            marker = ">" if index == selected else " "
            style = "bold black on cyan" if index == selected else ""
            table.add_row(Text(f"{marker} {value}", style=style), Text(label, style=style))

        footer = menu_hint(lang)
        if len(options) > window_size:
            footer = f"{footer}  {t(lang, 'showing')} {start + 1}-{start + len(visible_options)}/{len(options)}"

        console.print(
            Panel(
                Group(table, Text(footer, style="dim")),
                title=title,
                border_style="cyan",
                box=box.ROUNDED,
            )
        )

        key = read_key()
        if key in {"\x1b[A", "k"}:
            selected = (selected - 1) % len(options)
        elif key in {"\x1b[B", "j"}:
            selected = (selected + 1) % len(options)
        elif key in {"\r", "\n"}:
            return options[selected][0].lower()
        elif allow_quit and key.lower() in {"q", "\x03"}:
            return "q"
        else:
            for index, (value, _) in enumerate(options):
                if key.lower() == value.lower():
                    selected = index
                    return value.lower()


def choice_menu(
    lang: str,
    title: str,
    options: list[tuple[str, str]],
    header: Callable[[], None] | None = None,
    allow_quit: bool = True,
    default_value: str = "",
) -> str:
    return select_menu(
        lang,
        title,
        options,
        header=header,
        allow_quit=allow_quit,
        default_value=default_value,
    )


def menu_panel(lang: str, title: str, rows: Iterable[tuple[str, str]]) -> str:
    table = Table.grid(padding=(0, 2))
    table.add_column(style="bold cyan", no_wrap=True)
    table.add_column()
    for key, label in rows:
        table.add_row(key, label)
    console.print(Panel(table, title=title, border_style="cyan", box=box.ROUNDED))
    return ask(t(lang, "select")).lower()


def data_table(title: str, columns: list[str], rows: list[list[str]]) -> None:
    table = Table(title=title, box=box.SIMPLE_HEAVY, header_style="bold cyan")
    for column in columns:
        table.add_column(column)
    for row in rows:
        table.add_row(*[str(cell) for cell in row])
    console.print(table)


def form_panel(title: str, rows: list[tuple[str, str]], active_label: str = "") -> None:
    table = Table.grid(padding=(0, 2))
    table.add_column(style="bold cyan", no_wrap=True)
    table.add_column()
    for label, value in rows:
        shown = value if value else "-"
        style = "bold black on cyan" if label == active_label else ""
        table.add_row(Text(label, style=style), Text(shown, style=style))
    console.print(Panel(table, title=title, border_style="green", box=box.ROUNDED))


def field_display(field: dict) -> str:
    value = field.get("value", "")
    if field.get("kind") == "choice":
        for choice_value, choice_label in field.get("choices", []):
            if str(choice_value) == str(value):
                return str(choice_label)
    if field.get("secret") and value:
        return "*" * 8
    if field.get("kind") == "multiline" and value:
        lines = str(value).splitlines()
        preview = " / ".join(lines[:2])
        if len(lines) > 2:
            preview = f"{preview} ..."
        return preview[:70]
    return str(value)


def dialog_form_panel(
    lang: str,
    title: str,
    fields: list[dict],
    selected: int,
    message: str = "",
    allow_cancel: bool = True,
) -> None:
    table = Table.grid(padding=(0, 2))
    table.add_column(no_wrap=True)
    table.add_column(style="bold cyan", no_wrap=True)
    table.add_column()
    for index, field in enumerate(fields):
        marker = ">" if index == selected else " "
        required = " *" if field.get("required") else ""
        style = "bold black on cyan" if index == selected else ""
        table.add_row(
            Text(marker, style=style),
            Text(f"{field['label']}{required}", style=style),
            Text(field_display(field) or "-", style=style),
        )

    footer = (
        f"{t(lang, 'form_hint_move')}  "
        f"{t(lang, 'form_hint_edit')}  "
        f"{t(lang, 'form_hint_save')}"
    )
    if allow_cancel:
        footer = f"{footer}  {t(lang, 'form_hint_cancel')}"
    body = [table, Text(footer, style="dim")]
    if message:
        body.append(Text(message, style="yellow"))
    console.print(
        Panel(
            Group(*body),
            title=title,
            border_style="green",
            box=box.DOUBLE,
        )
    )


def field_choice_label(field: dict, value: str) -> str:
    for choice_value, choice_label in field.get("choices", []):
        if str(choice_value) == str(value):
            return str(choice_label)
    return value


def value_line_col(value: str, position: int) -> tuple[int, int]:
    before = value[:position]
    lines = before.split("\n")
    return len(lines) - 1, len(lines[-1])


def value_position_for_line_col(value: str, line: int, column: int) -> int:
    lines = value.split("\n")
    line = max(0, min(line, len(lines) - 1))
    position = 0
    for index in range(line):
        position += len(lines[index]) + 1
    return position + min(column, len(lines[line]))


def move_multiline_cursor(value: str, position: int, delta_line: int) -> int:
    line, column = value_line_col(value, position)
    return value_position_for_line_col(value, line + delta_line, column)


def wrap_value(value: str, width: int) -> list[tuple[int, int, str]]:
    width = max(1, width)
    rows: list[tuple[int, int, str]] = []
    offset = 0
    logical_lines = value.split("\n")
    for line_index, logical_line in enumerate(logical_lines):
        line_start = offset
        if not logical_line:
            rows.append((line_start, line_start, ""))
        else:
            start = 0
            while start < len(logical_line):
                end = min(start + width, len(logical_line))
                if end < len(logical_line):
                    break_at = logical_line.rfind(" ", start, end + 1)
                    if break_at > start:
                        end = break_at
                text = logical_line[start:end]
                rows.append((line_start + start, line_start + end, text))
                start = end
                while start < len(logical_line) and logical_line[start] == " ":
                    start += 1
        offset += len(logical_line)
        if line_index < len(logical_lines) - 1:
            offset += 1
    return rows or [(0, 0, "")]


def wrapped_cursor_position(
    wrapped_rows: list[tuple[int, int, str]],
    position: int,
) -> tuple[int, int]:
    for index, (start, end, text) in enumerate(wrapped_rows):
        if start <= position <= end:
            return index, min(max(0, position - start), len(text))
    last_index = len(wrapped_rows) - 1
    return last_index, len(wrapped_rows[last_index][2])


def move_wrapped_cursor(value: str, position: int, delta_row: int, width: int) -> int:
    wrapped_rows = wrap_value(value, width)
    row, column = wrapped_cursor_position(wrapped_rows, position)
    target_row = max(0, min(row + delta_row, len(wrapped_rows) - 1))
    start, _end, text = wrapped_rows[target_row]
    return start + min(column, len(text))


def multiline_field_height(field: dict) -> int:
    return int(field.get("height") or 7)


def validate_single_dialog_field(lang: str, field: dict) -> str:
    value = str(field.get("value", "")).strip()
    if field.get("required") and not value:
        return f"{field['label']}: {t(lang, 'required')}"
    validator = field.get("validator")
    if value and validator and not validator(value):
        return t(lang, field.get("invalid_key", "invalid_choice"))
    normalizer = field.get("normalizer")
    if value and normalizer:
        value = normalizer(value)
    limit = field.get("limit")
    if limit:
        value = value[: int(limit)]
    field["value"] = value
    return ""


def validate_dialog_form(lang: str, fields: list[dict]) -> str:
    for field in fields:
        error = validate_single_dialog_field(lang, field)
        if error:
            return error
    values = {field["name"]: str(field.get("value", "")) for field in fields}
    for field in fields:
        match_name = field.get("matches")
        if not match_name:
            continue
        value = values.get(field["name"], "")
        match_value = values.get(str(match_name), "")
        if (value or match_value) and value != match_value:
            return t(lang, field.get("mismatch_key", "password_mismatch"))
    return ""


def save_dialog_result(lang: str, fields: list[dict]) -> tuple[dict | None, str]:
    message = validate_dialog_form(lang, fields)
    if message:
        return None, message
    return {field["name"]: field.get("value", "") for field in fields}, ""


def button_label_key(button: str) -> str:
    if button == "save":
        return "save_button"
    if button == "delete":
        return "delete_button"
    if button == "send":
        return "send_button"
    return "cancel_button"


def curses_dialog_form(
    lang: str,
    title: str,
    fields: list[dict],
    allow_cancel: bool,
    buttons: list[str] | None = None,
) -> tuple[str, dict | None]:
    import curses

    buttons = buttons or (["save", "cancel"] if allow_cancel else ["save"])

    def set_cursor_visibility(curses_module, visibility: int) -> None:
        try:
            curses_module.curs_set(visibility)
        except curses_module.error:
            if visibility != 1:
                try:
                    curses_module.curs_set(1)
                except curses_module.error:
                    pass

    def draw_text(stdscr, y: int, x: int, text: str, attr: int = 0, width: int | None = None) -> None:
        max_y, max_x = stdscr.getmaxyx()
        if y < 0 or y >= max_y or x >= max_x:
            return
        if x < 0:
            text = text[-x:]
            x = 0
        if width is None:
            width = max_x - x
        text = text[: max(0, min(width, max_x - x))]
        try:
            stdscr.addstr(y, x, text, attr)
        except curses.error:
            pass

    def draw_software_cursor(
        stdscr,
        position: tuple[int, int] | None,
        character: str,
    ) -> None:
        if position is None:
            return
        y, x = position
        max_y, max_x = stdscr.getmaxyx()
        if y < 0 or y >= max_y or x < 0 or x >= max_x:
            return
        character = character[:1] or " "
        try:
            stdscr.addstr(y, x, character, curses.A_NORMAL | curses.A_BOLD)
        except curses.error:
            pass

    def render(stdscr, focus: int, cursors: list[int], scrolls: list[int], message: str) -> None:
        stdscr.erase()
        max_y, max_x = stdscr.getmaxyx()
        width = min(max_x - 2, 92)
        left = max(0, (max_x - width) // 2)
        height = min(
            max_y - 1,
            max(
                12,
                7
                + len(fields) * 2
                + sum(
                    max(0, multiline_field_height(field) - 1)
                    for field in fields
                    if field.get("kind") == "multiline"
                ),
            ),
        )
        top = max(0, (max_y - height) // 2)
        field_left = left + min(24, max(14, width // 3))
        field_width = max(16, width - (field_left - left) - 3)
        cursor_position = None
        software_cursor_position = None
        software_cursor_character = " "

        stdscr.attron(curses.A_BOLD)
        try:
            stdscr.border()
        except curses.error:
            pass
        stdscr.attroff(curses.A_BOLD)
        draw_text(stdscr, top, left + 2, f" {title} ", curses.A_BOLD)

        y = top + 2
        for index, field in enumerate(fields):
            focused = focus == index
            attr = curses.A_REVERSE if focused else curses.A_NORMAL
            label = field["label"] + (" *" if field.get("required") else "")
            draw_text(stdscr, y, left + 2, label, curses.A_BOLD, field_left - left - 4)

            if field.get("kind") == "multiline":
                box_height = multiline_field_height(field)
                for row in range(box_height):
                    draw_text(stdscr, y + row, field_left, " " * field_width, attr if focused else curses.A_DIM)
                value = str(field.get("value", ""))
                wrapped_rows = wrap_value(value, field_width)
                cursor_line, cursor_col = wrapped_cursor_position(wrapped_rows, cursors[index])
                scroll_line = scrolls[index]
                if cursor_line < scroll_line:
                    scroll_line = cursor_line
                if cursor_line >= scroll_line + box_height:
                    scroll_line = cursor_line - box_height + 1
                scrolls[index] = scroll_line
                for row, (_start, _end, line_value) in enumerate(
                    wrapped_rows[scroll_line : scroll_line + box_height]
                ):
                    draw_text(
                        stdscr,
                        y + row,
                        field_left,
                        line_value,
                        attr if focused else curses.A_NORMAL,
                        field_width,
                    )
                if focused:
                    cursor_position = (
                        y + cursor_line - scroll_line,
                        min(field_left + cursor_col, field_left + field_width - 1),
                    )
                    software_cursor_position = cursor_position
                    if cursor_col < len(wrapped_rows[cursor_line][2]):
                        software_cursor_character = wrapped_rows[cursor_line][2][cursor_col]
                    else:
                        software_cursor_character = " "
                y += box_height + 1
                continue

            value = str(field.get("value", ""))
            if field.get("kind") == "choice":
                shown = field_choice_label(field, value)
            elif field.get("secret") and value:
                shown = "*" * len(value)
            else:
                shown = value

            cursor = cursors[index]
            scroll = scrolls[index]
            if cursor < scroll:
                scroll = cursor
            if cursor >= scroll + field_width:
                scroll = cursor - field_width + 1
            scrolls[index] = scroll
            visible = shown[scroll : scroll + field_width]
            draw_text(stdscr, y, field_left, " " * field_width, attr if focused else curses.A_DIM)
            draw_text(stdscr, y, field_left, visible, attr if focused else curses.A_NORMAL, field_width)
            if focused and field.get("kind") != "choice":
                visible_cursor = cursor - scroll
                cursor_position = (y, min(field_left + visible_cursor, field_left + field_width - 1))
                software_cursor_position = cursor_position
                if 0 <= visible_cursor < len(visible):
                    software_cursor_character = visible[visible_cursor]
                else:
                    software_cursor_character = " "
            y += 2

        button_y = min(max_y - 4, y + 1)
        button_x = left + 2
        for offset, button in enumerate(buttons):
            button_index = len(fields) + offset
            label = f"[ {t(lang, button_label_key(button))} ]"
            attr = curses.A_REVERSE | curses.A_BOLD if focus == button_index else curses.A_BOLD
            draw_text(stdscr, button_y, button_x, label, attr)
            button_x += len(label) + 3

        hint = f"{t(lang, 'form_hint_tab')}  {t(lang, 'form_hint_type')}  {t(lang, 'form_hint_save')}"
        if allow_cancel:
            hint = f"{hint}  {t(lang, 'form_hint_cancel')}"
        draw_text(stdscr, max_y - 3, 2, hint, curses.A_DIM)
        if message:
            draw_text(stdscr, max_y - 2, 2, message, curses.A_BOLD)
        draw_software_cursor(stdscr, software_cursor_position, software_cursor_character)
        if cursor_position is not None:
            set_cursor_visibility(curses, 0)
            stdscr.move(*cursor_position)
        else:
            set_cursor_visibility(curses, 0)
        stdscr.refresh()

    def run(stdscr):
        set_cursor_visibility(curses, 1)
        curses.noecho()
        curses.cbreak()
        stdscr.keypad(True)

        focus = 0
        cursors = [len(str(field.get("value", ""))) for field in fields]
        scrolls = [0 for _ in fields]
        message = ""

        def current_field_width() -> int:
            _max_y, max_x = stdscr.getmaxyx()
            width = min(max_x - 2, 92)
            left = max(0, (max_x - width) // 2)
            field_left = left + min(24, max(14, width // 3))
            return max(16, width - (field_left - left) - 3)

        while True:
            render(stdscr, focus, cursors, scrolls, message)
            message = ""
            key = stdscr.getch()
            field_focused = focus < len(fields)
            field = fields[focus] if field_focused else None

            if key == 9:
                focus = (focus + 1) % (len(fields) + len(buttons))
            elif key == curses.KEY_BTAB:
                focus = (focus - 1) % (len(fields) + len(buttons))
            elif key == curses.KEY_F2:
                result, message = save_dialog_result(lang, fields)
                if result is not None:
                    return "save", result
            elif allow_cancel and key in (27,) and "cancel" in buttons:
                return "cancel", None
            elif not field_focused:
                button = buttons[focus - len(fields)]
                if key in (10, 13, curses.KEY_ENTER):
                    if button == "cancel":
                        return "cancel", None
                    if button == "delete":
                        return "delete", None
                    result, message = save_dialog_result(lang, fields)
                    if result is not None:
                        return button, result
            elif field and field.get("kind") == "choice":
                choices = field.get("choices", [])
                current = str(field.get("value", ""))
                current_index = next((idx for idx, (value, _) in enumerate(choices) if str(value) == current), 0)
                if key in (curses.KEY_RIGHT, 32, 10, 13, curses.KEY_ENTER):
                    field["value"] = choices[(current_index + 1) % len(choices)][0]
                elif key == curses.KEY_LEFT:
                    field["value"] = choices[(current_index - 1) % len(choices)][0]
                elif 32 <= key <= 126:
                    typed = chr(key).lower()
                    for value, _ in choices:
                        if str(value).lower().startswith(typed):
                            field["value"] = value
                            break
            elif field:
                value = str(field.get("value", ""))
                cursor = cursors[focus]
                if key in (10, 13, curses.KEY_ENTER):
                    if field.get("kind") == "multiline":
                        field["value"] = value[:cursor] + "\n" + value[cursor:]
                        cursors[focus] = cursor + 1
                    else:
                        focus = (focus + 1) % (len(fields) + len(buttons))
                elif key == curses.KEY_LEFT:
                    cursors[focus] = max(0, cursor - 1)
                elif key == curses.KEY_RIGHT:
                    cursors[focus] = min(len(value), cursor + 1)
                elif key == curses.KEY_HOME:
                    if field.get("kind") == "multiline":
                        line, _ = value_line_col(value, cursor)
                        cursors[focus] = value_position_for_line_col(value, line, 0)
                    else:
                        cursors[focus] = 0
                elif key == curses.KEY_END:
                    if field.get("kind") == "multiline":
                        line, _ = value_line_col(value, cursor)
                        cursors[focus] = value_position_for_line_col(value, line, 10_000)
                    else:
                        cursors[focus] = len(value)
                elif key == curses.KEY_PPAGE and field.get("kind") == "multiline":
                    cursors[focus] = move_wrapped_cursor(value, cursor, -4, current_field_width())
                elif key == curses.KEY_NPAGE and field.get("kind") == "multiline":
                    cursors[focus] = move_wrapped_cursor(value, cursor, 4, current_field_width())
                elif key == curses.KEY_BACKSPACE or key in (8, 127):
                    if cursor > 0:
                        field["value"] = value[: cursor - 1] + value[cursor:]
                        cursors[focus] = cursor - 1
                elif key == curses.KEY_DC:
                    if cursor < len(value):
                        field["value"] = value[:cursor] + value[cursor + 1 :]
                elif field.get("kind") == "multiline" and key == curses.KEY_UP:
                    cursors[focus] = move_wrapped_cursor(value, cursor, -1, current_field_width())
                elif field.get("kind") == "multiline" and key == curses.KEY_DOWN:
                    cursors[focus] = move_wrapped_cursor(value, cursor, 1, current_field_width())
                elif 32 <= key <= 126:
                    char = chr(key)
                    field["value"] = value[:cursor] + char + value[cursor:]
                    cursors[focus] = cursor + 1

    return curses.wrapper(run)


def ask_form_value(
    lang: str,
    title: str,
    rows: list[tuple[str, str]],
    label: str,
    current: str = "",
    secret: bool = False,
) -> str:
    clear()
    form_panel(title, rows, active_label=label)
    prompt = f"{label} [{current}]" if current and not secret else label
    if secret:
        return ask_secret(prompt)
    return ask(prompt)


def edit_dialog_field(lang: str, title: str, fields: list[dict], index: int) -> None:
    field = fields[index]
    kind = field.get("kind", "text")
    if kind == "choice":
        value = choice_menu(
            lang,
            field["label"],
            field.get("choices", []),
            default_value=str(field.get("value", "")),
        )
        if value not in {"q", "quit", "exit"}:
            if value.isdigit():
                choices = field.get("choices", [])
                choice_index = int(value) - 1
                if 0 <= choice_index < len(choices):
                    value = str(choices[choice_index][0])
            field["value"] = value
        return
    if kind == "multiline":
        clear()
        dialog_form_panel(lang, title, fields, index)
        field["value"] = "\n".join(text_input_window(lang, str(field.get("value", ""))))
        return

    value = ask_form_value(
        lang,
        title,
        [(item["label"], field_display(item)) for item in fields],
        field["label"],
        str(field.get("value", "")),
        secret=bool(field.get("secret")),
    ).strip()
    if value:
        field["value"] = value


def dialog_form_action(
    lang: str,
    title: str,
    fields: list[dict],
    allow_cancel: bool = True,
    buttons: list[str] | None = None,
) -> tuple[str, dict | None]:
    if not is_interactive():
        while True:
            for index, field in enumerate(fields):
                while True:
                    edit_dialog_field(lang, title, fields, index)
                    error = validate_single_dialog_field(lang, field)
                    if not error:
                        break
                    line(error)
            result, message = save_dialog_result(lang, fields)
            if result is not None:
                break
            line(message)
        if buttons and set(buttons) != {"save", "cancel"}:
            default_button = "save" if "save" in buttons else next(
                (button for button in buttons if button != "cancel"),
                buttons[0],
            )
            choice = choice_menu(
                lang,
                title,
                [(button, t(lang, button_label_key(button))) for button in buttons],
                allow_quit=False,
                default_value=default_button,
            )
            if choice in {"cancel", "delete"}:
                return choice, None
            return choice, result
        return "save", result

    try:
        clear()
        return curses_dialog_form(lang, title, fields, allow_cancel, buttons)
    except Exception as exc:
        line(f"{t(lang, 'dialog_unavailable')}: {exc}")
        pause(lang)
        while True:
            for index, field in enumerate(fields):
                while True:
                    edit_dialog_field(lang, title, fields, index)
                    error = validate_single_dialog_field(lang, field)
                    if not error:
                        break
                    line(error)
            result, message = save_dialog_result(lang, fields)
            if result is not None:
                break
            line(message)
        if buttons and set(buttons) != {"save", "cancel"}:
            default_button = "save" if "save" in buttons else next(
                (button for button in buttons if button != "cancel"),
                buttons[0],
            )
            choice = choice_menu(
                lang,
                title,
                [(button, t(lang, button_label_key(button))) for button in buttons],
                allow_quit=False,
                default_value=default_button,
            )
            if choice in {"cancel", "delete"}:
                return choice, None
            return choice, result
        return "save", result


def dialog_form(
    lang: str,
    title: str,
    fields: list[dict],
    allow_cancel: bool = True,
) -> dict | None:
    action, result = dialog_form_action(lang, title, fields, allow_cancel)
    if action == "save":
        return result
    return None


def paginated(
    lang: str,
    items: list,
    title: str,
    render_page: Callable[[list, int, int], None],
    page_size: int | None = None,
) -> None:
    if not items:
        render_page([], 1, 1)
        return

    size = page_size or PAGE_SIZE
    page = 0
    total_pages = max(1, (len(items) + size - 1) // size)
    while True:
        start = page * size
        end = start + size
        render_page(items[start:end], page + 1, total_pages)
        if total_pages == 1:
            return

        def header() -> None:
            clear()
            render_page(items[start:end], page + 1, total_pages)

        choice = select_menu(
            lang,
            f"{title} - {t(lang, 'page')} {page + 1}/{total_pages}",
            [
                ("n", t(lang, "next_page")),
                ("p", t(lang, "previous_page")),
                ("q", t(lang, "menu_quit")),
            ],
            header=header,
            allow_quit=False,
        )
        if choice == "n" and page < total_pages - 1:
            page += 1
        elif choice == "p" and page > 0:
            page -= 1
        elif choice in {"q", "quit", "exit", ""}:
            return


def text_input_window(lang: str, current: str = "") -> list[str]:
    if current:
        console.print(
            Panel(
                current,
                title=t(lang, "current_text"),
                border_style="blue",
                box=box.ROUNDED,
            )
        )
    console.print(
        Panel(
            t(lang, "enter_message"),
            title=t(lang, "message_input_title"),
            border_style="green",
            box=box.ROUNDED,
        )
    )
    body_lines: list[str] = []
    while True:
        text = sys.stdin.readline()
        if not text:
            break
        text = text.rstrip("\r\n")
        if text == ".":
            break
        body_lines.append(text[:120])
    return body_lines
