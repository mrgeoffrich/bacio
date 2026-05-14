import { EditorContent } from '@tiptap/react';
import { useEffect, useState } from 'react';
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
  const [isInitialized, setIsInitialized] = useState(false);

  const editor = useEditorConfig({
    content,
    readOnly,
    onChange,
    isInitialized,
  });

  // Push prop content into the editor when it changes (e.g. loading a
  // different document). Skip no-op updates so typing doesn't reset.
  useEffect(() => {
    if (!editor) return;

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const currentContent = (editor.storage as any).markdown.getMarkdown();

    if (content !== currentContent) {
      editor.commands.setContent(content);
      setIsInitialized(true);
    }
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
