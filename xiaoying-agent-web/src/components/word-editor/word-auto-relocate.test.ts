import { describe, expect, it } from 'vitest';
import type { WordEditorApplyIssue } from './types';
import {
	MAX_WORD_AUTO_RELOCATE_ATTEMPTS,
	buildWordAutoRelocateFingerprint,
	buildWordAutoRelocateIssueKey,
	buildWordAutoRelocatePrompt,
	getWordAutoRelocatePrompt,
	isWordApplyIssueAutoRelocatable,
} from './word-auto-relocate';

describe('word-auto-relocate', () => {
	it('treats retryable apply issues as auto-relocatable', () => {
		const issue: WordEditorApplyIssue = {
			message: '命中 2 个候选段落，无法安全直接修改',
			occurred_at: 123,
			diagnostics: {
				error_code: 'AMBIGUOUS_TARGET',
				candidate_count: 2,
				retry_action: {
					action: 'replace',
					target_text: '通过率',
					new_text: '92%',
				},
				retry_prompt: '[WORD_AUTO_RELOCATE]\nretryable\n[/WORD_AUTO_RELOCATE]',
			},
		};

		expect(isWordApplyIssueAutoRelocatable(issue)).toBe(true);
		expect(isWordApplyIssueAutoRelocatable({
			...issue,
			message: '表格 tbl_4751 第 11 行的 cell_index=5 超出范围',
			diagnostics: {
				error_code: 'TABLE_STRUCTURE_CONFLICT',
				retryable: true,
				retry_action: {
					action: 'update_table_cell',
					target_text: '通过率',
				},
				retry_prompt: '[WORD_AUTO_RELOCATE]\nretryable\n[/WORD_AUTO_RELOCATE]',
			},
		})).toBe(true);
		expect(isWordApplyIssueAutoRelocatable({
			...issue,
			message: '表格仅剩 1 列，不能再删除',
			diagnostics: {
				error_code: 'TABLE_STRUCTURE_CONFLICT',
				retryable: false,
			},
		})).toBe(false);
		expect(MAX_WORD_AUTO_RELOCATE_ATTEMPTS).toBe(2);
	});

	it('builds a stable fingerprint and prompt payload', () => {
		const issue: WordEditorApplyIssue = {
			message: '命中 2 个候选段落，无法安全直接修改',
			occurred_at: 456,
			diagnostics: {
				error_code: 'AMBIGUOUS_TARGET',
				target_kind: 'paragraph',
				query: '通过率',
				scope: '第三章',
				retry_action: {
					action: 'replace',
					target_text: '通过率',
					new_text: '92%',
					target_selector: {
						type: 'paragraph_id_selector',
						paragraph_id: 'p-1',
					},
				},
				retry_prompt: '[WORD_AUTO_RELOCATE]\nerror_code: AMBIGUOUS_TARGET\ncandidate_summaries:\n1. 第三章 通过率 90%\n[/WORD_AUTO_RELOCATE]',
			},
		};

		const fingerprint = buildWordAutoRelocateFingerprint(issue);
		expect(fingerprint).toContain('AMBIGUOUS_TARGET');
		expect(buildWordAutoRelocateIssueKey(issue)).toBe('456:AMBIGUOUS_TARGET:命中 2 个候选段落，无法安全直接修改');

		const prompt = buildWordAutoRelocatePrompt(issue);
		expect(prompt).toContain('[WORD_AUTO_RELOCATE]');
		expect(prompt).toContain('第三章 通过率 90%');
		expect(getWordAutoRelocatePrompt(issue)).toBe(prompt);
	});

	it('builds a recovery prompt for retryable table structure conflicts', () => {
		const issue: WordEditorApplyIssue = {
			message: '表格 tbl_4751 第 11 行的 cell_index=5 超出范围',
			occurred_at: 789,
			diagnostics: {
				error_code: 'TABLE_STRUCTURE_CONFLICT',
				target_kind: 'table_cell',
				message: '表格 tbl_4751 第 11 行的 cell_index=5 超出范围',
				retryable: true,
				retry_action: {
					action: 'update_table_cell',
					target_text: '通过率',
					target_selector: {
						type: 'table_cell_selector',
						table_id: 'tbl_4751',
						row_index: 10,
						cell_index: 5,
					},
				},
				retry_prompt: '[WORD_AUTO_RELOCATE]\nerror_code: TABLE_STRUCTURE_CONFLICT\n必须重新读取目标表格的最新结构\n[/WORD_AUTO_RELOCATE]',
			},
		};

		const prompt = buildWordAutoRelocatePrompt(issue);
		expect(prompt).toContain('error_code: TABLE_STRUCTURE_CONFLICT');
		expect(prompt).toContain('必须重新读取目标表格的最新结构');
	});
});