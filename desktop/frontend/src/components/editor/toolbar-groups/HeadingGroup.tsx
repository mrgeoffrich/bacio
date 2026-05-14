import { Editor } from '@tiptap/react';
import { Heading1, Heading2, Heading3 } from 'lucide-react';
import { ToolbarButton } from '../ToolbarButton';

interface HeadingGroupProps {
  editor: Editor;
}

export function HeadingGroup({ editor }: HeadingGroupProps) {
  return (
    <div className="toolbar-group">
      <ToolbarButton
        onClick={() => editor.chain().focus().toggleHeading({ level: 1 }).run()}
        active={editor.isActive('heading', { level: 1 })}
        title="Heading 1"
      >
        <Heading1 className="toolbar-icon" />
      </ToolbarButton>
      <ToolbarButton
        onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}
        active={editor.isActive('heading', { level: 2 })}
        title="Heading 2"
      >
        <Heading2 className="toolbar-icon" />
      </ToolbarButton>
      <ToolbarButton
        onClick={() => editor.chain().focus().toggleHeading({ level: 3 }).run()}
        active={editor.isActive('heading', { level: 3 })}
        title="Heading 3"
      >
        <Heading3 className="toolbar-icon" />
      </ToolbarButton>
    </div>
  );
}
