import { Editor, useEditorState } from '@tiptap/react';
import { Undo, Redo } from 'lucide-react';
import { ToolbarButton } from '../ToolbarButton';

interface UndoRedoGroupProps {
  editor: Editor;
}

// BACI-223: derive button state inside useEditorState's selector so we
// only touch `editor.can()` once per transaction AND can null-check
// `editor.commandManager` first. TipTap nulls `commandManager` in
// Editor.destroy(), and there's a brief window during navigation
// (deep-link mount → loading=true unmounts NotionEditor → load
// resolves and remounts) where a paint can land on the destroyed
// editor instance. The selector short-circuits, the render reads two
// plain booleans, and the toolbar no longer throws
// `null is not an object (evaluating 'this.commandManager.can')`.
export function UndoRedoGroup({ editor }: UndoRedoGroupProps) {
  const { canUndo, canRedo } = useEditorState({
    editor,
    selector: ({ editor: ed }) => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      if (!ed || ed.isDestroyed || !(ed as any).commandManager) {
        return { canUndo: false, canRedo: false };
      }
      return { canUndo: ed.can().undo(), canRedo: ed.can().redo() };
    },
  });

  return (
    <div className="toolbar-group">
      <ToolbarButton
        onClick={() => editor.chain().focus().undo().run()}
        disabled={!canUndo}
        title="Undo"
      >
        <Undo className="toolbar-icon" />
      </ToolbarButton>
      <ToolbarButton
        onClick={() => editor.chain().focus().redo().run()}
        disabled={!canRedo}
        title="Redo"
      >
        <Redo className="toolbar-icon" />
      </ToolbarButton>
    </div>
  );
}
