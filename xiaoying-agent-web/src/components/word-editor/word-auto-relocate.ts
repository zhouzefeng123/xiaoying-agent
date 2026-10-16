import type { GoWordActionDiagnostics, WordEditorActionPayload, WordEditorApplyIssue } from './types';

export const MAX_WORD_AUTO_RELOCATE_ATTEMPTS = 2;

const LEGACY_AUTO_RELOCATE_ERROR_CODES = new Set(['AMBIGUOUS_TARGET']);

function normalizeText(value?: string | null): string {
	return value?.replace(/\s+/g, ' ').trim() ?? '';
}

function pickDiagnostics(issue: WordEditorApplyIssue): GoWordActionDiagnostics | undefined {
	return issue.diagnostics ?? issue.plan?.diagnostics;
}

function isRetryableIssue(diagnostics?: GoWordActionDiagnostics): boolean {
	if (!diagnostics) {
		return false;
	}
	if (diagnostics.retryable === true) {
		return true;
	}
	return LEGACY_AUTO_RELOCATE_ERROR_CODES.has((diagnostics.error_code ?? '').trim().toUpperCase());
}
export function getWordAutoRelocateAction(issue: WordEditorApplyIssue): WordEditorActionPayload | null {
	return pickDiagnostics(issue)?.retry_action ?? issue.action ?? issue.plan?.replay_action ?? null;
}

export function getWordAutoRelocatePrompt(issue: WordEditorApplyIssue): string {
	return normalizeText(pickDiagnostics(issue)?.retry_prompt);
}

export function isWordApplyIssueAutoRelocatable(issue: WordEditorApplyIssue | null | undefined): issue is WordEditorApplyIssue {
	if (!issue) {
		return false;
	}

	const diagnostics = pickDiagnostics(issue);
	if (!isRetryableIssue(diagnostics)) {
		return false;
	}

	return Boolean(getWordAutoRelocateAction(issue)) && getWordAutoRelocatePrompt(issue).length > 0;
}

export function buildWordAutoRelocateIssueKey(issue: WordEditorApplyIssue): string {
	return `${issue.occurred_at}:${pickDiagnostics(issue)?.error_code ?? ''}:${normalizeText(issue.message)}`;
}

export function buildWordAutoRelocateFingerprint(issue: WordEditorApplyIssue): string {
	const action = getWordAutoRelocateAction(issue);
	const diagnostics = pickDiagnostics(issue);
	return JSON.stringify({
		action: action ? {
			action: action.action,
			target_text: normalizeText(action.target_text),
			new_text: normalizeText(action.new_text),
			preferred_paragraph_id: action.preferred_paragraph_id ?? '',
			table_id: action.table_id ?? '',
			row_index: action.row_index ?? null,
			cell_index: action.cell_index ?? null,
			target_selector: action.target_selector ?? null,
		} : null,
		diagnostics: {
			error_code: diagnostics?.error_code ?? '',
			target_kind: diagnostics?.target_kind ?? '',
			query: normalizeText(diagnostics?.query),
			scope: normalizeText(diagnostics?.scope),
			retry_prompt: normalizeText(diagnostics?.retry_prompt),
		},
	});
}

export function buildWordAutoRelocatePrompt(issue: WordEditorApplyIssue): string {
	return getWordAutoRelocatePrompt(issue);
}