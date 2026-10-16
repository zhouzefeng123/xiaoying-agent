/**
 * Office Engine Adapter (Mock for Phase 2)
 * 该适配器用于屏蔽底层 Web Office（如 OnlyOffice/Collabora/Syncfusion）的繁杂 API，
 * 对外暴露适用于 Agent 精确控制的简单接口。
 */

export interface OfficeAdapter {
  init(containerId: string, documentUrl: string): Promise<void>;
  enableTrackChanges(enable: boolean): Promise<void>;
  replaceText(targetText: string, newText: string): Promise<boolean>;
  insertText(anchorText: string, newText: string, position: 'before' | 'after'): Promise<boolean>;
  deleteText(targetText: string): Promise<boolean>;
  scrollTo(targetText: string): Promise<boolean>;
}

export class MockOfficeAdapter implements OfficeAdapter {
  private container: HTMLElement | null = null;

  private escapeRegExp(text: string): string {
	return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }

  private findTextElement(text: string): HTMLElement | null {
	if (!this.container) {
	  return null;
	}

	const elements = Array.from(this.container.querySelectorAll<HTMLElement>('p, div, span'));
	return elements.find((element) => element.textContent?.includes(text)) ?? null;
  }

  async init(containerId: string, documentUrl: string): Promise<void> {
    this.container = document.getElementById(containerId);
    if (this.container) {
      this.container.innerHTML = `<div class="mx-auto min-h-[700px] max-w-[780px] bg-white px-[88px] py-[96px] shadow-[0_20px_60px_rgba(80,56,30,0.12)]">
        <p class="mb-10 text-center text-[11px] tracking-[0.3em] text-[#8e7b68]">${documentUrl || '尚未加载文档'}</p>
        <h1 class="mb-10 text-center text-[28px] font-semibold text-[#3f2d1f]">XiaoYing for Word 高保真编辑预览</h1>
        <p class="mb-5 text-[16px] leading-8 text-[#2f241b]">这是一个测试文本，用于模拟 Agent 精确定位后的替换与修订效果。</p>
        <p class="mb-5 text-[16px] leading-8 text-[#2f241b]">第二段用于模拟插入和删除操作，保留商务文档排版与右侧审阅联动能力。</p>
        <p class="text-[16px] leading-8 text-[#2f241b]">本区域稍后将被 OnlyOffice / Collabora 实际画布接管。</p>
      </div>`;
      console.log(`[OfficeAdapter] Initialized with document: ${documentUrl}`);
    }
  }

  async enableTrackChanges(enable: boolean): Promise<void> {
    console.log(`[OfficeAdapter] Track Changes mode: ${enable ? 'ENABLED' : 'DISABLED'}`);
    // 真实场景下调用如: window.Asc.plugin.executeMethod("StartTrackChanges")
  }

  async replaceText(targetText: string, newText: string): Promise<boolean> {
    console.log(`[OfficeAdapter] Replacing '${targetText}' with '${newText}'`);
    if (this.container) {
      const content = this.container.innerHTML;
      if (content.includes(targetText)) {
         const diffMarkup = `<span data-word-editor-anchor="latest-change"><del class="text-red-500 bg-red-100">${targetText}</del>${newText ? `<ins class="text-green-600 bg-green-100 font-medium no-underline">${newText}</ins>` : ''}</span>`;
         this.container.innerHTML = content.replace(
           new RegExp(this.escapeRegExp(targetText)),
           diffMarkup
         );
         return true;
      }
    }
    return false;
  }

  async insertText(anchorText: string, newText: string, position: 'before' | 'after'): Promise<boolean> {
    console.log(`[OfficeAdapter] Inserting text ${position} '${anchorText}'`);
    const anchorElement = this.findTextElement(anchorText);
    if (!anchorElement) {
	  return false;
    }

	const paragraphMarkup = `<p data-word-editor-anchor="latest-change" class="my-5 text-[16px] leading-8 text-[#2f241b]"><ins class="bg-green-100 text-green-700 no-underline">${newText}</ins></p>`;
	anchorElement.insertAdjacentHTML(position === 'before' ? 'beforebegin' : 'afterend', paragraphMarkup);
	return true;
  }

  async deleteText(targetText: string): Promise<boolean> {
    console.log(`[OfficeAdapter] Deleting '${targetText}'`);
    return this.replaceText(targetText, '');
  }

  async scrollTo(targetText: string): Promise<boolean> {
    console.log(`[OfficeAdapter] Scrolling to '${targetText}'`);
    const latestAnchor = this.container?.querySelector<HTMLElement>('[data-word-editor-anchor="latest-change"]');
  const targetElement = latestAnchor ?? this.findTextElement(targetText);
  if (!targetElement) {
    return false;
  }

  targetElement.scrollIntoView({ behavior: 'smooth', block: 'center' });
  targetElement.classList.add('ring-2', 'ring-amber-300', 'transition');
  window.setTimeout(() => {
    targetElement.classList.remove('ring-2', 'ring-amber-300');
  }, 1800);
  return true;
  }
}
