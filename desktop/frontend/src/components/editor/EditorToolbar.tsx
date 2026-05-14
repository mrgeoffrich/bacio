import { Editor } from '@tiptap/react';
import { UndoRedoGroup } from './toolbar-groups/UndoRedoGroup';
import { HeadingGroup } from './toolbar-groups/HeadingGroup';
import { FormattingGroup } from './toolbar-groups/FormattingGroup';
import { ListGroup } from './toolbar-groups/ListGroup';
import { TableGroup } from './toolbar-groups/TableGroup';
import { ToolbarDivider } from './toolbar-groups/ToolbarDivider';

interface EditorToolbarProps {
  editor: Editor;
}

export function EditorToolbar({ editor }: EditorToolbarProps) {
  return (
    <div className="notion-toolbar">
      <UndoRedoGroup editor={editor} />
      <ToolbarDivider />
      <HeadingGroup editor={editor} />
      <ToolbarDivider />
      <FormattingGroup editor={editor} />
      <ToolbarDivider />
      <ListGroup editor={editor} />
      <ToolbarDivider />
      <TableGroup editor={editor} />
    </div>
  );
}
