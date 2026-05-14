import { Editor } from '@tiptap/react';
import { BubbleMenu } from '@tiptap/react/menus';
import { HeadingGroup } from './toolbar-groups/HeadingGroup';
import { FormattingGroup } from './toolbar-groups/FormattingGroup';
import { ListGroup } from './toolbar-groups/ListGroup';
import { TableGroup } from './toolbar-groups/TableGroup';
import { BubbleMenuDivider } from './toolbar-groups/BubbleMenuDivider';

interface EditorBubbleMenuProps {
  editor: Editor;
}

export function EditorBubbleMenu({ editor }: EditorBubbleMenuProps) {
  return (
    <BubbleMenu editor={editor}>
      <div className="bubble-menu">
        <HeadingGroup editor={editor} />
        <BubbleMenuDivider />
        <FormattingGroup editor={editor} />
        <BubbleMenuDivider />
        <ListGroup editor={editor} />
        <BubbleMenuDivider />
        <TableGroup editor={editor} />
      </div>
    </BubbleMenu>
  );
}
