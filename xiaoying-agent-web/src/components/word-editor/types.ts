export type WordEditorActionType = 'replace' | 'insert' | 'insert_paragraph_break' | 'insert_line_break' | 'insert_table' | 'insert_table_row' | 'delete_table_row' | 'insert_table_column' | 'delete_table_column' | 'update_table_cell' | 'delete' | 'scroll' | 'format' | 'format_paragraph' | 'format_section' | 'update_header_footer';

export interface GoWordActionTargetSelector {
	type: 'paragraph_id_selector' | 'heading_scope_selector' | 'table_selector' | 'table_cell_selector';
	paragraph_id?: string;
	table_id?: string;
	row_index?: number;
	cell_index?: number;
	heading_level?: number;
	ordinal?: number;
	chapter?: string;
	text_equals?: string;
	region?: string;
}

export interface GoWordActionConstraints {
	raw_text?: string;
	must_be_unique?: boolean;
	preserve_style?: boolean;
	preserve_numbering?: boolean;
}

export interface GoWordActionFailurePolicy {
	on_ambiguous?: 'return_candidates' | 'return_error';
	on_validation_failed?: 'report_warning' | 'rollback_and_report';
}

export interface WordEditorActionPayload {
	action: WordEditorActionType;
	target_text: string;
	target_selector?: GoWordActionTargetSelector;
	preferred_paragraph_id?: string;
	table_id?: string;
	row_index?: number;
	cell_index?: number;
	style_patch?: GoWordTextStyle;
	paragraph_style_patch?: GoWordParagraphStyle;
	table_spec?: GoWordTableInput;
	table_row?: GoWordTableRowInput;
	table_column?: GoWordTableColumnInput;
	table_cell?: GoWordTableCellInput;
	header_footer?: GoWordHeaderFooterInput;
	section_layout_patch?: GoWordSectionLayoutPatch;
	new_text?: string;
	position?: 'before' | 'after';
	enable_track_changes?: boolean;
	edit_intent?: string;
	context_scope?: 'line' | 'paragraph' | 'table' | 'all';
	constraints?: string | GoWordActionConstraints;
	failure_policy?: GoWordActionFailurePolicy;
	preview_only?: boolean;
	retrieval_query?: string;
	require_retrieval?: boolean;
	reader_session_id?: string;
	reader_context_summary?: string;
	format_basis?: string;
	exploration_summary?: string;
}

export interface WordEditorDocumentState {
	name: string;
	url: string;
	source: 'local' | 'remote' | 'mock' | 'uploaded';
	path?: string;
	file?: File;
}

export interface GoWordTextStyle {
	style_id?: string;
	bold?: boolean;
	bold_set?: boolean;
	italic?: boolean;
	italic_set?: boolean;
	underline?: boolean;
	underline_set?: boolean;
	color?: string;
	theme_color?: string;
	font_family?: string;
	theme_font_ascii?: string;
	theme_font_east_asia?: string;
	font_size_hp?: number;
}

export interface GoWordRunImage {
	relationship_id?: string;
	target?: string;
	filename?: string;
	content_type?: string;
	name?: string;
	width_emu?: number;
	height_emu?: number;
	inline?: boolean;
}

export interface GoWordRun {
	id: string;
	text?: string;
	break_type?: 'line' | 'page' | 'renderedPage' | 'textWrapping';
	style: GoWordTextStyle;
	drawing_xml?: string;
	image?: GoWordRunImage;
}

export interface GoWordNumberingRef {
	num_id: string;
	level?: number;
}

export interface GoWordParagraphStyle {
	style_id?: string;
	alignment?: string;
	spacing_before?: number;
	spacing_after?: number;
	line_spacing?: number;
	first_line_indent?: number;
	left_indent?: number;
	keep_next?: boolean;
	keep_next_set?: boolean;
	keep_lines?: boolean;
	keep_lines_set?: boolean;
	page_break_before?: boolean;
	page_break_before_set?: boolean;
	widow_control?: boolean;
	widow_control_set?: boolean;
	numbering?: GoWordNumberingRef;
}

export interface GoWordParagraph {
	id: string;
	runs: GoWordRun[];
	style: GoWordParagraphStyle;
	numbering_text?: string;
	section_break_layout?: GoWordSectionLayoutPatch;
}

export interface GoWordParagraphInput {
	text: string;
	runs?: GoWordRun[];
	style?: GoWordParagraphStyle;
}

export interface GoWordTableCellInput {
	properties_xml?: string;
	paragraphs?: GoWordParagraphInput[];
}

export interface GoWordTableRowInput {
	properties_xml?: string;
	repeat_header?: boolean;
	repeat_header_set?: boolean;
	cant_split?: boolean;
	cant_split_set?: boolean;
	cells: GoWordTableCellInput[];
}

export interface GoWordTableColumnInput {
	cells: GoWordTableCellInput[];
}

export interface GoWordTableInput {
	properties_xml?: string;
	grid_xml?: string;
	rows: GoWordTableRowInput[];
}

export interface GoWordBlockInput {
	type: 'paragraph' | 'table';
	paragraph?: GoWordParagraphInput;
	table?: GoWordTableInput;
}

export interface GoWordHeaderFooterInput {
	kind: 'header' | 'footer';
	variant?: 'default' | 'first' | 'even';
	blocks: GoWordBlockInput[];
}

export interface GoWordSectionLayoutPatch {
	break_type?: string;
	page_width?: number;
	page_height?: number;
	orientation?: string;
	title_page?: boolean;
	title_page_set?: boolean;
	page_number_start?: number;
	page_number_format?: string;
	margin_top?: number;
	margin_right?: number;
	margin_bottom?: number;
	margin_left?: number;
	margin_header?: number;
	margin_footer?: number;
	margin_gutter?: number;
}

export interface GoWordTableCell {
	id: string;
	properties_xml?: string;
	blocks?: GoWordBlock[];
}

export interface GoWordTableRow {
	id: string;
	properties_xml?: string;
	repeat_header?: boolean;
	repeat_header_set?: boolean;
	cant_split?: boolean;
	cant_split_set?: boolean;
	cells: GoWordTableCell[];
}

export interface GoWordTable {
	id: string;
	properties_xml?: string;
	grid_xml?: string;
	rows: GoWordTableRow[];
}

export interface GoWordBlock {
	type: 'paragraph' | 'table';
	paragraph?: GoWordParagraph;
	table?: GoWordTable;
}

export interface GoWordHeaderFooterPart {
	kind: 'header' | 'footer';
	variant?: 'default' | 'first' | 'even';
	relationship_id?: string;
	target?: string;
	blocks?: GoWordBlock[];
	paragraphs?: GoWordParagraph[];
}

export interface GoWordDocumentSection {
	start_block_index?: number;
	end_block_index?: number;
	layout?: GoWordSectionLayoutPatch;
	header_footers?: GoWordHeaderFooterPart[];
}

export interface GoWordDocumentMeta {
	format?: string;
	section_layout?: GoWordSectionLayoutPatch;
	sections?: GoWordDocumentSection[];
	header_footers?: GoWordHeaderFooterPart[];
}

export interface GoWordDocument {
	id: string;
	name: string;
	source_path?: string;
	revision: number;
	blocks?: GoWordBlock[];
	paragraphs: GoWordParagraph[];
	meta?: GoWordDocumentMeta;
}

export type GoWordDiffType = 'unchanged' | 'inserted' | 'deleted' | 'updated';

export interface GoWordCharacterRangeDiff {
	type: GoWordDiffType;
	before_start?: number;
	before_end?: number;
	after_start?: number;
	after_end?: number;
	before_text?: string;
	after_text?: string;
}

export interface GoWordParagraphRunSnapshot {
	run_id?: string;
	kind: 'text' | 'image';
	text?: string;
	style?: GoWordTextStyle;
	image?: GoWordRunImage;
}

export interface GoWordParagraphDiff {
	paragraph_id: string;
	type: GoWordDiffType;
	before_text?: string;
	after_text?: string;
	before_runs?: GoWordParagraphRunSnapshot[];
	after_runs?: GoWordParagraphRunSnapshot[];
	character_ranges?: GoWordCharacterRangeDiff[];
}

export interface GoWordSearchHit {
	type: 'line' | 'paragraph' | 'table';
	paragraph_id?: string;
	table_id?: string;
	row_index?: number;
	cell_index?: number;
	line_index?: number;
	text: string;
	match_start?: number;
	match_end?: number;
}

export interface GoWordSearchResult {
	query: string;
	scope: string;
	mode: string;
	total: number;
	limit: number;
	truncated: boolean;
	hits: GoWordSearchHit[];
}

export interface GoWordSession {
	id: string;
	document: GoWordDocument;
	updated_at: string;
}

export interface GoWordApplyResult {
	document: GoWordDocument;
	diffs: GoWordParagraphDiff[];
}

export interface GoWordIRNode {
	node_id: string;
	type: string;
	region?: string;
	path?: string[];
	heading_scope?: string[];
	paragraph_id?: string;
	table_id?: string;
	row_index?: number;
	cell_index?: number;
	heading_level?: number;
	style_id?: string;
	numbering_text?: string;
	text?: string;
	preview?: string;
}

export interface GoWordIROutlineNode {
	paragraph_id?: string;
	text?: string;
	heading_level?: number;
	path?: string[];
	heading_scope?: string[];
	style_id?: string;
	numbering_text?: string;
}

export interface GoWordIRDocument {
	document_id: string;
	name: string;
	revision: number;
	nodes: GoWordIRNode[];
	outline?: GoWordIROutlineNode[];
}

export interface GoWordActionCandidate {
	target_kind?: string;
	paragraph_id?: string;
	table_id?: string;
	row_index?: number;
	cell_index?: number;
	region?: string;
	path?: string[];
	heading_scope?: string[];
	preview?: string;
	matched_text?: string;
	match_start?: number;
	match_end?: number;
	score?: number;
}

export interface GoWordActionDiagnostics {
	error_code?: string;
	message?: string;
	target_kind?: string;
	query?: string;
	candidate_count?: number;
	candidates?: GoWordActionCandidate[];
	scope?: string;
	attempted_strategies?: string[];
	recommended_actions?: string[];
	retryable?: boolean;
	retry_action?: WordEditorActionPayload;
	retry_prompt?: string;
}

export interface GoWordResolvedTarget {
	target_kind?: string;
	paragraph_id?: string;
	table_id?: string;
	row_index?: number;
	cell_index?: number;
	region?: string;
	path?: string[];
	heading_scope?: string[];
	match_start?: number;
	match_end?: number;
	preview?: string;
}

export interface GoWordActionValidation {
	ok: boolean;
	message?: string;
	error_code?: string;
	diagnostics?: GoWordActionDiagnostics;
}

export interface GoWordActionPlanResult {
	status: 'ready' | 'noop' | 'failed';
	status_text?: string;
	expected_revision?: number;
	operations?: import('./goword-client').GoWordOperation[];
	active_paragraph_id?: string;
	replay_action: WordEditorActionPayload;
	resolved_target?: GoWordResolvedTarget;
	diagnostics?: GoWordActionDiagnostics;
}

export interface GoWordActionExecutionResult {
	status: 'applied' | 'applied_with_warning' | 'preview' | 'noop' | 'failed';
	status_text?: string;
	document?: GoWordDocument;
	diffs?: GoWordParagraphDiff[];
	active_paragraph_id?: string;
	replay_action: WordEditorActionPayload;
	plan?: GoWordActionPlanResult;
	validation: GoWordActionValidation;
	diagnostics?: GoWordActionDiagnostics;
}

export interface WordEditorApplyIssue {
	message: string;
	occurred_at: number;
	diagnostics?: GoWordActionDiagnostics;
	action?: WordEditorActionPayload;
	plan?: GoWordActionPlanResult;
}

export interface GoWordSaveResult {
	path: string;
	document: GoWordDocument;
	updated_at: string;
}

export interface GoWordLayoutOptions {
	page?: {
		width_twips?: number;
		height_twips?: number;
		margin_top_twips?: number;
		margin_right_twips?: number;
		margin_bottom_twips?: number;
		margin_left_twips?: number;
		header_distance_twips?: number;
		footer_distance_twips?: number;
	};
	start_page?: number;
	include_debug?: boolean;
}

export interface GoWordLayoutRun {
	run_id?: string;
	text?: string;
	break_type?: 'line' | 'page' | 'renderedPage' | 'textWrapping';
	width_twips?: number;
	start_twips?: number;
	has_drawing?: boolean;
}

export interface GoWordLayoutLine {
	index: number;
	text?: string;
	top_twips?: number;
	height_twips?: number;
	runs?: GoWordLayoutRun[];
}

export interface GoWordLayoutParagraph {
	paragraph_id?: string;
	line_count?: number;
	lines?: GoWordLayoutLine[];
}

export interface GoWordLayoutTableCell {
	cell_id?: string;
	paragraphs?: GoWordLayoutParagraph[];
	block_count?: number;
}

export interface GoWordLayoutTableRow {
	row_id?: string;
	top_twips?: number;
	height_twips?: number;
	repeat_header?: boolean;
	cant_split?: boolean;
	cells?: GoWordLayoutTableCell[];
}

export interface GoWordLayoutTable {
	table_id?: string;
	row_count?: number;
	rows?: GoWordLayoutTableRow[];
}

export interface GoWordLayoutBlock {
	id?: string;
	type: 'paragraph' | 'table';
	source_block_index: number;
	top_twips?: number;
	height_twips?: number;
	paragraph?: GoWordLayoutParagraph;
	table?: GoWordLayoutTable;
}

export interface GoWordLayoutPage {
	index: number;
	number: number;
	width_twips: number;
	height_twips: number;
	content_top_twips: number;
	content_bottom_twips: number;
	blocks?: GoWordLayoutBlock[];
}

export interface GoWordDocumentLayout {
	session_id?: string;
	document_id?: string;
	revision?: number;
	generated_at: string;
	page_spec: {
		width_twips?: number;
		height_twips?: number;
		margin_top_twips?: number;
		margin_right_twips?: number;
		margin_bottom_twips?: number;
		margin_left_twips?: number;
		header_distance_twips?: number;
		footer_distance_twips?: number;
	};
	engine: {
		name: string;
		version?: string;
	};
	pages: GoWordLayoutPage[];
	warnings?: string[];
}

export interface WordEditorState {
	isOpen: boolean;
	document: WordEditorDocumentState | null;
	goWordSessionId: string | null;
	lastApplyError: string | null;
	lastApplyErrorAt: number | null;
	lastApplyIssue: WordEditorApplyIssue | null;
	lastAction: WordEditorActionPayload | null;
	actionHistory: WordEditorActionPayload[];
	actionVersion: number;
	hasUnsavedChanges: boolean;
}