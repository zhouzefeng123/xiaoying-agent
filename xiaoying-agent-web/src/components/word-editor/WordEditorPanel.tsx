import { FileText, Loader2, Upload, X } from 'lucide-react';
import { forwardRef, useCallback, useImperativeHandle, useRef, useState, type ChangeEvent } from 'react';
import { WordPreview, type WordPreviewHandle, type WordPreviewToolbarState } from './WordPreview';
import { closeWordEditor, openLocalWordDocument, useWordEditorState } from './word-editor-bus';

export interface WordEditorPanelHandle {
	requestClose: () => Promise<boolean>;
}

interface WordEditorPanelProps {}

export const WordEditorPanel = forwardRef<WordEditorPanelHandle, WordEditorPanelProps>(function WordEditorPanel(_props, ref) {
	const fileInputRef = useRef<HTMLInputElement>(null);
	const previewRef = useRef<WordPreviewHandle>(null);
	const state = useWordEditorState();
	const [toolbarState, setToolbarState] = useState<WordPreviewToolbarState>({
		canSave: false,
		isBusy: false,
		isSaving: false,
		sessionId: null,
	});

	const handleOpenClick = useCallback(async () => {
		if (window.electron?.selectFiles) {
			const selectedFiles = await window.electron.selectFiles();
			const docFile = selectedFiles?.find((file) => /\.(doc|docx)$/i.test(file.name));
			if (docFile) {
				const blob = new Blob([], { type: docFile.type || 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' });
				const file = new File([blob], docFile.name, { type: docFile.type || blob.type });
				(file as File & { _absolutePath?: string })._absolutePath = docFile.path;
				openLocalWordDocument(file);
				return;
			}
		}

		fileInputRef.current?.click();
	}, []);

	const handleFileChange = (event: ChangeEvent<HTMLInputElement>) => {
		const file = event.target.files?.[0];
		if (!file) {
			return;
		}

		openLocalWordDocument(file);
		event.target.value = '';
	};

	const requestClose = useCallback(async (): Promise<boolean> => {
		if (toolbarState.isSaving) {
			return false;
		}

		if (state.hasUnsavedChanges) {
			let decision = 2;
			if (window.electron?.showMessageBox) {
				const result = await window.electron.showMessageBox({
					type: 'question',
					title: '关闭 Word 面板',
					message: '当前文档有未保存的修改。',
					detail: '你可以先保存，再关闭窗口。',
					buttons: ['保存', '不保存', '取消'],
					defaultId: 0,
					cancelId: 2,
					noLink: true,
				});
				decision = result.response;
			} else if (typeof window.confirm === 'function') {
				if (window.confirm('当前文档有未保存的修改。是否先保存再关闭？')) {
					decision = 0;
				} else if (window.confirm('是否不保存并直接关闭？')) {
					decision = 1;
				}
			}

			if (decision === 2) {
				return false;
			}

			if (decision === 0) {
				const saved = await previewRef.current?.saveDocument(false);
				if (!saved) {
					return false;
				}
			}
		}

		closeWordEditor();
		return true;
	}, [state.hasUnsavedChanges, toolbarState.isSaving]);

	useImperativeHandle(ref, () => ({
		requestClose,
	}), [requestClose]);

	const handleClose = async () => {
		if (!(await requestClose())) {
			return;
		}
	};

	const handleSaveClick = (saveAs: boolean) => {
		void previewRef.current?.saveDocument(saveAs);
	};

	return (
		<div className="flex h-full min-w-0 flex-col border-l border-[#d9d0c4] bg-[#f5f1ea]">
			<div className="flex items-center justify-between border-b border-[#d9d0c4] bg-[#fbf8f2] px-4 py-3">
				<div className="min-w-0">
					<div className="flex items-center gap-2 text-sm font-semibold text-[#4b3928]">
						<FileText className="h-4 w-4" />
						<span>XiaoYing For Word（Preview）</span>
					</div>
					<p className="truncate text-xs text-[#7f6a57]">
						{state.document?.name ?? '未打开 Word 文档'}
					</p>
				</div>
				<div className="flex items-center gap-2">
					<button
						onClick={() => {
							handleSaveClick(false);
						}}
						disabled={!toolbarState.canSave || toolbarState.isBusy || toolbarState.isSaving}
						className="rounded-md border border-[#d8c7b3] bg-white px-3 py-1.5 text-xs font-medium text-[#5d4836] transition enabled:hover:bg-[#f5ede3] disabled:cursor-not-allowed disabled:opacity-45"
					>
						保存
					</button>
					<button
						onClick={() => {
							handleSaveClick(true);
						}}
						disabled={!toolbarState.canSave || toolbarState.isBusy || toolbarState.isSaving}
						className="rounded-md border border-[#d8c7b3] bg-white px-3 py-1.5 text-xs font-medium text-[#5d4836] transition enabled:hover:bg-[#f5ede3] disabled:cursor-not-allowed disabled:opacity-45"
					>
						另存为
					</button>
					<button
						onClick={() => {
							void handleOpenClick();
						}}
						className="rounded-md border border-[#d8c7b3] bg-white px-3 py-1.5 text-xs font-medium text-[#5d4836] transition hover:bg-[#f5ede3]"
					>
						打开文档
					</button>
					{(toolbarState.isBusy || toolbarState.isSaving) && <Loader2 className="h-4 w-4 animate-spin text-[#7f6a57]" />}
					<button
						onClick={handleClose}
						className="rounded-md p-1.5 text-[#6c5a4a] transition hover:bg-[#efe6dc]"
						title="关闭 Word 面板"
					>
						<X className="h-4 w-4" />
					</button>
				</div>
				<input
					ref={fileInputRef}
					type="file"
					accept=".doc,.docx"
					onChange={handleFileChange}
					className="hidden"
				/>
			</div>

			<div className="min-h-0 flex-1">
				<WordPreview
					ref={previewRef}
					document={state.document}
					lastAction={state.lastAction}
					actionVersion={state.actionVersion}
					onToolbarStateChange={setToolbarState}
				/>
			</div>

			<div className="border-t border-[#e5d9ca] bg-[#fffaf3] px-4 py-2 text-xs text-[#7b6453]">
				<div className="flex items-center gap-2">
					<Upload className="h-3.5 w-3.5" />
					<span>当前面板与 Agent 指令完全联动；打开真实 docx 后将执行真实编辑并回显差异。</span>
				</div>
			</div>
		</div>
	);
});
