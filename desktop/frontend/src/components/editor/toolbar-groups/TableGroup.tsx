import { Editor } from '@tiptap/react';
import { Table as TableIcon, Plus, Minus, Trash2 } from 'lucide-react';
import { ToolbarButton } from '../ToolbarButton';

interface TableGroupProps {
  editor: Editor;
}

export function TableGroup({ editor }: TableGroupProps) {
  const inTable = editor.isActive('table');
  return (
    <div className="toolbar-group">
      <ToolbarButton
        onClick={() => editor.chain().focus().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run()}
        disabled={inTable}
        title="Insert Table"
      >
        <TableIcon className="toolbar-icon" />
      </ToolbarButton>
      {inTable && (
        <>
          <ToolbarButton
            onClick={() => editor.chain().focus().addColumnBefore().run()}
            title="Add Column Before"
          >
            <Plus className="toolbar-icon" />
          </ToolbarButton>
          <ToolbarButton
            onClick={() => editor.chain().focus().addRowBefore().run()}
            title="Add Row Before"
          >
            <Plus className="toolbar-icon" />
          </ToolbarButton>
          <ToolbarButton
            onClick={() => editor.chain().focus().deleteColumn().run()}
            title="Delete Column"
          >
            <Minus className="toolbar-icon" />
          </ToolbarButton>
          <ToolbarButton
            onClick={() => editor.chain().focus().deleteRow().run()}
            title="Delete Row"
          >
            <Minus className="toolbar-icon" />
          </ToolbarButton>
          <ToolbarButton
            onClick={() => editor.chain().focus().deleteTable().run()}
            title="Delete Table"
          >
            <Trash2 className="toolbar-icon" />
          </ToolbarButton>
        </>
      )}
    </div>
  );
}
