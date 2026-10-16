import { useEffect, useRef, useState } from 'react';
import type { WordEditorActionPayload, WordEditorApplyIssue, WordEditorDocumentState, WordEditorState } from './types';

type WordEditorListener = (state: WordEditorState) => void;

const initialState: WordEditorState = {
	isOpen: false,
	document: null,
	goWordSessionId: null,
	lastApplyError: null,
	lastApplyErrorAt: null,
	lastApplyIssue: null,
	lastAction: null,
	actionHistory: [],
	actionVersion: 0,
	hasUnsavedChanges: false,
};

function normalizeApplyIssue(issue: string | WordEditorApplyIssue | null): WordEditorApplyIssue | null {
	if (typeof issue === 'string') {
		const message = issue.trim();
		if (!message) {
			return null;
		}
		return {
			message,
			occurred_at: Date.now(),
		};
	}

	if (!issue) {
		return null;
	}

	const message = issue.message?.trim();
	if (!message) {
		return null;
	}

	return {
		...issue,
		message,
		occurred_at: Number.isFinite(issue.occurred_at) ? issue.occurred_at : Date.now(),
	};
}

class WordEditorBus {
	private state: WordEditorState = initialState;
	private listeners = new Set<WordEditorListener>();

	getState(): WordEditorState {
		return this.state;
	}

	subscribe(listener: WordEditorListener): () => void {
		this.listeners.add(listener);
		listener(this.state);
		return () => {
			this.listeners.delete(listener);
		};
	}

	open(document?: WordEditorDocumentState | null): void {
		if (document) {
			this.replaceDocument(document);
			return;
		}

		this.state = {
			...this.state,
			isOpen: true,
		};
		this.emit();
	}

	close(): void {
		this.state = {
			...this.state,
			isOpen: false,
			goWordSessionId: null,
			lastApplyError: null,
			lastApplyErrorAt: null,
			lastApplyIssue: null,
			hasUnsavedChanges: false,
		};
		this.emit();
	}

	replaceDocument(document: WordEditorDocumentState): void {
		this.revokeBlobURLIfNeeded(this.state.document?.url, document.url);
		this.state = {
			...this.state,
			isOpen: true,
			document,
			goWordSessionId: null,
			lastApplyError: null,
			lastApplyErrorAt: null,
			lastApplyIssue: null,
			hasUnsavedChanges: false,
		};
		this.emit();
	}

	updateGoWordSession(sessionId: string | null): void {
		if (this.state.goWordSessionId === sessionId) {
			return;
		}
		this.state = {
			...this.state,
			goWordSessionId: sessionId,
		};
		this.emit();
	}

	publishAction(action: WordEditorActionPayload): void {
		this.state = {
			...this.state,
			isOpen: true,
			lastAction: action,
			actionHistory: [action, ...this.state.actionHistory].slice(0, 20),
			actionVersion: this.state.actionVersion + 1,
		};
		this.emit();
	}

	updateUnsavedChanges(hasChanges: boolean): void {
		if (this.state.hasUnsavedChanges === hasChanges) {
			return;
		}
		this.state = {
			...this.state,
			hasUnsavedChanges: hasChanges,
		};
		this.emit();
	}

	updateLastApplyError(issue: string | WordEditorApplyIssue | null): void {
		const normalizedIssue = normalizeApplyIssue(issue);
		const normalized = normalizedIssue?.message ?? null;
		if (this.state.lastApplyError === normalized) {
			if (!normalizedIssue && !this.state.lastApplyIssue) {
				return;
			}
			if (normalizedIssue && this.state.lastApplyIssue?.occurred_at === normalizedIssue.occurred_at) {
				return;
			}
		}
		if (!normalized && !this.state.lastApplyIssue) {
			return;
		}
		this.state = {
			...this.state,
			lastApplyError: normalized,
			lastApplyErrorAt: normalizedIssue?.occurred_at ?? null,
			lastApplyIssue: normalizedIssue,
		};
		this.emit();
	}

	private emit(): void {
		for (const listener of this.listeners) {
			listener(this.state);
		}
	}

	private revokeBlobURLIfNeeded(previousURL?: string | null, nextURL?: string | null): void {
		if (!previousURL || previousURL === nextURL || !previousURL.startsWith('blob:')) {
			return;
		}

		URL.revokeObjectURL(previousURL);
	}
}

export const wordEditorBus = new WordEditorBus();

export function useWordEditorState(): WordEditorState {
	const [state, setState] = useState<WordEditorState>(wordEditorBus.getState());

	useEffect(() => wordEditorBus.subscribe(setState), []);

	return state;
}

/**
 * 选择器版本：仅当 selector 返回值发生变化时才触发重渲染。
 * 用于只关心 WordEditor 部分字段（例如 isOpen）的组件，避免 Word 内部高频更新
 * （publishAction / updateGoWordSession 等）冒泡到聊天主树造成不必要的重渲染。
 */
export function useWordEditorSelector<T>(
	selector: (state: WordEditorState) => T,
	isEqual: (a: T, b: T) => boolean = Object.is,
): T {
	const selectorRef = useRef(selector);
	const isEqualRef = useRef(isEqual);
	selectorRef.current = selector;
	isEqualRef.current = isEqual;

	const [selected, setSelected] = useState<T>(() => selector(wordEditorBus.getState()));

	useEffect(() => {
		return wordEditorBus.subscribe((nextState) => {
			const nextSelected = selectorRef.current(nextState);
			setSelected((prev) => (isEqualRef.current(prev, nextSelected) ? prev : nextSelected));
		});
	}, []);

	return selected;
}

export function openWordEditor(document?: WordEditorDocumentState | null): void {
	wordEditorBus.open(document);
}

export function closeWordEditor(): void {
	wordEditorBus.close();
}

export function loadMockWordDocument(): void {
	wordEditorBus.open({
		name: '示例商务文档.docx',
		url: 'mock://business-word-demo',
		source: 'mock',
	});
}

export function openLocalWordDocument(file: File): void {
	const absolutePath = (file as File & { _absolutePath?: string; path?: string })._absolutePath
		?? (file as File & { path?: string }).path;
	const blobURL = absolutePath ?? URL.createObjectURL(file);
	wordEditorBus.open({
		name: file.name,
		url: blobURL,
		source: 'local',
		path: absolutePath,
		file,
	});
}

export function setWordUnsavedChanges(hasChanges: boolean): void {
	wordEditorBus.updateUnsavedChanges(hasChanges);
}

export function publishWordEditorAction(action: WordEditorActionPayload): void {
	wordEditorBus.publishAction(action);
}

export function publishWordEditorSession(sessionId: string | null): void {
	wordEditorBus.updateGoWordSession(sessionId);
}

export function publishWordEditorApplyError(issue: string | WordEditorApplyIssue | null): void {
	wordEditorBus.updateLastApplyError(issue);
}

export function getWordEditorStateSnapshot(): WordEditorState {
	return wordEditorBus.getState();
}