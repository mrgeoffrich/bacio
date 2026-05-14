import { Editor } from '@tiptap/react';
import { List, ListOrdered, Quote } from 'lucide-react';
import { ToolbarButton } from '../ToolbarButton';

interface ListGroupProps {
  editor: Editor;
}

export function ListGroup({ editor }: ListGroupProps) {
  return (
    <div className="toolbar-group">
      <ToolbarButton
        onClick={() => editor.chain().focus().toggleBulletList().run()}
        active={editor.isActive('bulletList')}
        title="Bullet List"
      >
        <List className="toolbar-icon" />
      </ToolbarButton>
      <ToolbarButton
        onClick={() => editor.chain().focus().toggleOrderedList().run()}
        active={editor.isActive('orderedList')}
        title="Numbered List"
      >
        <ListOrdered className="toolbar-icon" />
      </ToolbarButton>
      <ToolbarButton
        onClick={() => editor.chain().focus().toggleBlockquote().run()}
        active={editor.isActive('blockquote')}
        title="Quote"
      >
        <Quote className="toolbar-icon" />
      </ToolbarButton>
    </div>
  );
}
