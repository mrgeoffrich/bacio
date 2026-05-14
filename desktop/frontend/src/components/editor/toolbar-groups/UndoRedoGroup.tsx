import { Editor } from '@tiptap/react';
import { Undo, Redo } from 'lucide-react';
import { ToolbarButton } from '../ToolbarButton';

interface UndoRedoGroupProps {
  editor: Editor;
}

export function UndoRedoGroup({ editor }: UndoRedoGroupProps) {
  return (
    <div className="toolbar-group">
      <ToolbarButton
        onClick={() => editor.chain().focus().undo().run()}
        disabled={!editor.can().undo()}
        title="Undo"
      >
        <Undo className="toolbar-icon" />
      </ToolbarButton>
      <ToolbarButton
        onClick={() => editor.chain().focus().redo().run()}
        disabled={!editor.can().redo()}
        title="Redo"
      >
        <Redo className="toolbar-icon" />
      </ToolbarButton>
    </div>
  );
}
