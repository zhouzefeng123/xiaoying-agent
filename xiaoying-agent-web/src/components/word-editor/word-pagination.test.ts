import { describe, expect, it } from 'vitest';
import type { GoWordDocumentSection, GoWordHeaderFooterPart, GoWordParagraph } from './types';
import {
  buildPageNumberContexts,
  pickHeaderFooterPart,
  resolvePageLayout,
  sectionBreakForcesPageBreak,
  sectionStartNeedsBlankPage,
} from './word-pagination';

describe('word-pagination', () => {
  it('uses first header/footer for the first page inside a section', () => {
    const parts: GoWordHeaderFooterPart[] = [
      { kind: 'header', variant: 'default', paragraphs: [] },
      { kind: 'header', variant: 'first', paragraphs: [] },
    ];

    const result = pickHeaderFooterPart(parts, 'header', {
      sectionPageOrdinal: 1,
      displayedPageNumber: 5,
    }, {
      title_page: true,
    });

    expect(result?.variant).toBe('first');
  });

  it('restarts displayed page number when section defines a start value', () => {
    const sections: Array<GoWordDocumentSection | null> = [
      { start_block_index: 0, end_block_index: 0, layout: {} },
      { start_block_index: 0, end_block_index: 0, layout: {} },
      { start_block_index: 1, end_block_index: 1, layout: { page_number_start: 7 } },
      { start_block_index: 1, end_block_index: 1, layout: { page_number_start: 7 } },
    ];

    const contexts = buildPageNumberContexts(sections);

    expect(contexts.map((context) => context.displayedPageNumber)).toEqual([1, 2, 7, 8]);
    expect(contexts.map((context) => context.sectionPageOrdinal)).toEqual([1, 2, 1, 2]);
  });

  it('inserts parity blank pages for odd and even section starts', () => {
    expect(sectionStartNeedsBlankPage({ break_type: 'oddPage' }, 2)).toBe(true);
    expect(sectionStartNeedsBlankPage({ break_type: 'oddPage' }, 3)).toBe(false);
    expect(sectionStartNeedsBlankPage({ break_type: 'evenPage' }, 1)).toBe(true);
    expect(sectionStartNeedsBlankPage({ break_type: 'evenPage' }, 2)).toBe(false);
  });

  it('keeps continuous section on the same page when page geometry matches', () => {
    const paragraph: GoWordParagraph = {
      id: 'p1',
      runs: [],
      style: {},
      section_break_layout: {
        break_type: 'continuous',
        page_width: 11906,
        page_height: 16838,
      },
    };

    const currentSection: GoWordDocumentSection = {
      start_block_index: 0,
      end_block_index: 0,
      layout: {
        page_width: 11906,
        page_height: 16838,
      },
    };

    expect(sectionBreakForcesPageBreak(paragraph, currentSection)).toBe(false);
  });

  it('forces a new page for continuous section when page geometry changes', () => {
    const paragraph: GoWordParagraph = {
      id: 'p1',
      runs: [],
      style: {},
      section_break_layout: {
        break_type: 'continuous',
        page_width: 16838,
        page_height: 11906,
        orientation: 'landscape',
      },
    };

    const currentSection: GoWordDocumentSection = {
      start_block_index: 0,
      end_block_index: 0,
      layout: {
        page_width: 11906,
        page_height: 16838,
        orientation: 'portrait',
      },
    };

    expect(sectionBreakForcesPageBreak(paragraph, currentSection)).toBe(true);
    expect(resolvePageLayout(paragraph.section_break_layout).orientation).toBe('landscape');
  });
});