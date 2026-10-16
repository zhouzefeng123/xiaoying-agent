import React, { useEffect, useMemo, useRef } from 'react';
import Editor, { type OnMount } from '@monaco-editor/react';
import 'monaco-editor/min/vs/editor/editor.main.css';
import * as monaco from 'monaco-editor';

interface CodeEditorProps {
    value: string;
    onChange: (value: string) => void;
    onSave: () => void;
    language?: string;
    readOnly?: boolean;
    path?: string; // Add path prop for Monaco model URI
}

export const CodeEditor = React.memo<CodeEditorProps>(({
    value,
    onChange,
    onSave,
    language = 'text',
    readOnly = false,
    path
}) => {
    const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
    const monacoRef = useRef<typeof monaco | null>(null);

    const getMonacoLanguage = (lang: string) => {
        const map: Record<string, string> = {
            'js': 'javascript',
            'jsx': 'javascript',
            'ts': 'typescript',
            'tsx': 'typescript',
            'py': 'python',
            'pyw': 'python',
            'go': 'go',
            'rs': 'rust',
            'rust': 'rust',
            'html': 'html',
            'htm': 'html',
            'css': 'css',
            'scss': 'scss',
            'less': 'less',
            'json': 'json',
            'jsonc': 'json',
            'md': 'markdown',
            'markdown': 'markdown',
            'sql': 'sql',
            'java': 'java',
            'c': 'c',
            'cpp': 'cpp',
            'cxx': 'cpp',
            'cc': 'cpp',
            'h': 'cpp',
            'hpp': 'cpp',
            'cs': 'csharp',
            'csharp': 'csharp',
            'sh': 'shell',
            'bash': 'shell',
            'zsh': 'shell',
            'fish': 'shell',
            'ps1': 'powershell',
            'pwsh': 'powershell',
            'yml': 'yaml',
            'yaml': 'yaml',
            'xml': 'xml',
            'svg': 'xml',
            'dockerfile': 'dockerfile',
            'docker': 'dockerfile',
            'ini': 'ini',
            'conf': 'ini',
            'toml': 'ini',
            'bat': 'bat',
            'cmd': 'bat',
            'php': 'php',
            'rb': 'ruby',
            'ruby': 'ruby',
            'lua': 'lua',
            'r': 'r',
            'swift': 'swift',
            'kt': 'kotlin',
            'kotlin': 'kotlin',
            'dart': 'dart',
            'vb': 'vb',
            'vbs': 'vb',
            'vue': 'html', // Fallback for Vue
            'svelte': 'html', // Fallback for Svelte
            'txt': 'plaintext',
            'text': 'plaintext',
            'log': 'plaintext'
        };
        const normalized = lang.toLowerCase();
        return map[normalized] || normalized;
    };

    const resolvedLanguage = useMemo(() => getMonacoLanguage(language), [language]);

    const syncModelLanguage = (
        editorInstance: monaco.editor.IStandaloneCodeEditor | null,
        monacoInstance: typeof monaco | null,
        nextLanguage: string
    ) => {
        if (!editorInstance || !monacoInstance) {
            return;
        }

        const model = editorInstance.getModel();
        if (!model || model.getLanguageId() === nextLanguage) {
            return;
        }

        // 强制同步模型语言，避免 Monaco 复用 plaintext 模型导致语法高亮失效。
        monacoInstance.editor.setModelLanguage(model, nextLanguage);
    };

    const handleEditorDidMount: OnMount = (editor, monaco) => {
        editorRef.current = editor;
        monacoRef.current = monaco;
        
        syncModelLanguage(editor, monaco, resolvedLanguage);

        // Add Save command (Ctrl+S / Cmd+S)
        editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
            onSave();
        });
    };

    useEffect(() => {
        syncModelLanguage(editorRef.current, monacoRef.current, resolvedLanguage);
    }, [resolvedLanguage, path]);

    return (
        <Editor
            height="100%"
            path={path} // Pass path to Monaco
            defaultLanguage="plaintext"
            language={resolvedLanguage}
            value={value}
            theme="vs-dark"
            onChange={(val) => onChange(val || '')}
            onMount={handleEditorDidMount}
            options={{
                minimap: { enabled: true },
                fontSize: 14,
                fontFamily: 'JetBrains Mono, Menlo, Monaco, Consolas, monospace',
                scrollBeyondLastLine: false,
                automaticLayout: true,
                padding: { top: 16, bottom: 16 },
                readOnly: readOnly,
                wordWrap: 'on',
            }}
        />
    );
}, (prevProps, nextProps) => {
    // Custom comparison for React.memo
    // Only re-render if these props change
    return prevProps.value === nextProps.value &&
           prevProps.language === nextProps.language &&
           prevProps.readOnly === nextProps.readOnly &&
           prevProps.path === nextProps.path; // Check path
});
