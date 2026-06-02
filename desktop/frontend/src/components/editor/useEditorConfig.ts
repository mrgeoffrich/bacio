import { useEditor } from '@tiptap/react';
import type { MutableRefObject } from 'react';
import StarterKit from '@tiptap/starter-kit';
import Placeholder from '@tiptap/extension-placeholder';
import TaskList from '@tiptap/extension-task-list';
import TaskItem from '@tiptap/extension-task-item';
import { Table } from '@tiptap/extension-table';
import { TableRow } from '@tiptap/extension-table-row';
import { TableCell } from '@tiptap/extension-table-cell';
import { TableHeader } from '@tiptap/extension-table-header';
import { Markdown } from 'tiptap-markdown';

interface UseEditorConfigProps {
  content: string;
  readOnly: boolean;
  onChange: (content: string) => void;
  // Live "has the initial content been seeded" flag. Read through a ref,
  // not a captured boolean: useEditor builds the editor (and its onUpdate
  // closure) exactly once, so a plain prop would freeze at its first-render
  // value (false) and permanently swallow every onChange (BACI-340).
  initializedRef: MutableRefObject<boolean>;
}

export function useEditorConfig({ content, readOnly, onChange, initializedRef }: UseEditorConfigProps) {
  return useEditor({
    extensions: [
      StarterKit.configure({
        heading: {
          levels: [1, 2, 3],
        },
      }),
      Placeholder.configure({
        placeholder: ({ node }) => {
          if (node.type.name === 'heading') {
            return 'Heading';
          }
          return 'Start writing…';
        },
      }),
      TaskList,
      TaskItem.configure({
        nested: true,
        HTMLAttributes: {
          class: 'task-item',
        },
      }),
      Table.configure({
        resizable: true,
        HTMLAttributes: {
          class: 'tiptap-table',
        },
      }),
      TableRow,
      TableHeader,
      TableCell,
      Markdown.configure({
        html: true,
        tightLists: true,
        transformPastedText: true,
        transformCopiedText: true,
      }),
    ],
    content,
    editable: !readOnly,
    onUpdate: ({ editor }) => {
      if (!initializedRef.current) {
        return;
      }
      // tiptap-markdown stores its serializer on editor.storage.markdown.
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const markdown = (editor.storage as any).markdown.getMarkdown();
      onChange(markdown);
    },
    editorProps: {
      attributes: {
        class: 'notion-editor-content',
      },
    },
  });
}
