import { autocompletion, type CompletionContext } from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { sql, MySQL } from "@codemirror/lang-sql";
import { EditorState } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { oneDark } from "@codemirror/theme-one-dark";

export function createEditor(
  parent: HTMLElement,
  run: () => void,
  schemaNames: () => string[],
): EditorView {
  const completions = (context: CompletionContext) => {
    const word = context.matchBefore(/[\w.]+/);
    if (!word && !context.explicit) return null;
    return {
      from: word?.from ?? context.pos,
      options: schemaNames().map((label) => ({ label, type: "variable" })),
    };
  };

  return new EditorView({
    parent,
    state: EditorState.create({
      doc: "SELECT *\nFROM ",
      extensions: [
        history(),
        sql({ dialect: MySQL }),
        autocompletion({ override: [completions] }),
        keymap.of([
          ...defaultKeymap,
          ...historyKeymap,
          { key: "Mod-Enter", preventDefault: true, run: () => (run(), true) },
        ]),
        EditorView.lineWrapping,
        oneDark,
      ],
    }),
  });
}

export function selectedQuery(view: EditorView): string {
  const selection = view.state.selection.main;
  if (!selection.empty) return view.state.sliceDoc(selection.from, selection.to).trim();
  return view.state.doc.toString().trim();
}

export function setQuery(view: EditorView, query: string): void {
  view.dispatch({
    changes: { from: 0, to: view.state.doc.length, insert: query },
    selection: { anchor: query.length },
  });
  view.focus();
}
