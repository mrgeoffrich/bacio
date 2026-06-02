import { EditorContent } from '@tiptap/react';
import { useEffect, useRef } from 'react';
import { useEditorConfig } from './useEditorConfig';
import { EditorToolbar } from './EditorToolbar';
import { EditorBubbleMenu } from './EditorBubbleMenu';
import './notion-editor.css';

interface NotionEditorProps {
  content: string;
  onChange: (content: string) => void;
  readOnly?: boolean;
}

export function NotionEditor({ content, onChange, readOnly = false }: NotionEditorProps) {
  const editable = !readOnly;
  // "Has the initial prop content been seeded into the editor yet" — held in
  // a ref, not state, because useEditorConfig's onUpdate closure is built
  // once and would capture a stale boolean forever (BACI-340). The guard
  // suppresses the onUpdate that tiptap v3 fires from the programmatic seed
  // setContent; without it the seed would echo back as a spurious onChange.
  const initializedRef = useRef(false);

  const editor = useEditorConfig({
    content,
    readOnly,
    onChange,
    initializedRef,
  });

  // Push prop content into the editor when it changes (e.g. loading a
  // different document, or an external CLI/API edit). Skip no-op updates so
  // typing doesn't reset. emitUpdate:false keeps the programmatic write from
  // round-tripping through onUpdate as if the user had typed it — tiptap v3
  // emits an update from setContent by default, which would otherwise feed
  // the incoming body straight back into onChange.
  useEffect(() => {
    if (!editor) return;

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const currentContent = (editor.storage as any).markdown.getMarkdown();

    if (content !== currentContent) {
      editor.commands.setContent(content, { emitUpdate: false });
    }
    // The editor now holds the prop content; real keystrokes from here on
    // are genuine user edits and must reach onChange.
    initializedRef.current = true;
  }, [editor, content]);

  useEffect(() => {
    if (editor) {
      editor.setEditable(!readOnly);
    }
  }, [editor, readOnly]);

  if (!editor) {
    return null;
  }

  return (
    <div className="notion-editor-wrapper">
      {editable && <EditorToolbar editor={editor} />}
      {editable && <EditorBubbleMenu editor={editor} />}
      <EditorContent editor={editor} />
    </div>
  );
}
