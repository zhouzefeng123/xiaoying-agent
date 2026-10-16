"""
@tool pdf-maker-pro
@description 专业商务 PDF 生成器：将 Markdown 文本自动转换为排版精美的 PDF 文档。
@param markdown_text string (必填) 输入的 Markdown 格式字符串（支持#、##、-、正文、表格、![](path) 图片插入）
@param output_filename string (可选) 输出文件路径（相对或绝对路径）。如果不提供，将自动生成在当前目录下。
@param template string (可选) 模板风格，可选值："business_clean"（默认）、"report_formal"。
@param fit_image boolean (可选) 是否自动按比例缩放图片以适应页面，默认为 True。
@param max_image_width integer (可选) 图片最大宽度（单位：pt），默认为 450。页面可用宽度约为 450pt。
@param max_image_height integer (可选) 图片最大高度（单位：pt），默认为 550。
@param compression_quality integer (可选) 图片压缩质量（1-100，默认 85）。仅当图片过大需要压缩时生效。

# PDF-Maker-Pro Skill

将结构化 Markdown 文本本地渲染为高质量、排版专业的 PDF 文档。
完全支持中文字体（自动检测系统字体），支持图片插入与自动缩放，适用于生成报告、企划书、会议纪要等商务场景。

## 核心特性
- **中文完美支持**: 自动加载系统自带的中文字体（如 Microsoft YaHei/SimHei），杜绝乱码。
- **智能图片处理**: 支持 Markdown 图片语法 `![](path)`，自动按比例缩放图片以适应页面宽度和高度，防止溢出。
- **参数化图片控制**: 
  - 支持全局设置 `max_image_width` 和 `max_image_height`。
  - 支持在 Markdown Alt Text 中指定单张图片尺寸，语法：`![Caption|w=300,h=200](path)`。
- **图片优化**: 支持调整图片压缩质量，平衡文件大小与清晰度。
- **Emoji & 图标**: 支持直接插入标准 Unicode Emoji (如 🚀, ✅, 📅)，自动适配 Segoe UI Emoji 字体，增强文档表现力。
- **专业排版**: 内置商务风格模板，包含页眉、页脚、页码、标题层级样式。
- **Markdown 解析**: 支持一级/二级/三级标题、无序列表、引用、**表格**、普通段落。
- **表格渲染**: 支持标准 Markdown 表格语法，自动美化表头与交替行背景色。

## 参数详细说明 (Parameters)
1. `markdown_text` (Required):
   - 核心内容输入，支持标准 Markdown 语法。
   - 标题: `# Title`, `## Heading 1`, `### Heading 2`
   - 列表: `- Item 1`, `* Item 2`
   - 图片: `![](C:/path/to/image.png)` (建议使用绝对路径)
   - 文本: 普通段落文本，支持自动换行。

2. `output_filename` (Optional):
   - 指定输出文件名，如 `report.pdf` 或 `D:/docs/plan.pdf`。
   - 默认为 `document_YYYYMMDD_HHMMSS.pdf`。

3. `template` (Optional):
   - `business_clean`: 默认商务风格，简洁蓝黑配色。
   - `report_formal`: (预留) 更正式的报告风格。

4. `fit_image` (Optional):
   - `true`: (默认) 自动按比例缩放图片以适应页面打印区域（宽度和高度），防止图片过大或跨页截断。
   - `false`: 仅限制图片宽度不超过页面，允许高度超出（可能导致分页问题）。

5. `max_image_width` & `max_image_height` (Optional):
   - 全局限制图片的最大宽度和高度（单位：点 pt）。
   - 默认值：Width=450, Height=550。
   - 可以在 Markdown Alt Text 中覆盖，如 `![Caption|w=300,h=200](path)`。

6. `compression_quality` (Optional):
   - 图片压缩质量，范围 1-100，默认为 85。
   - 设置较低值可减小生成的 PDF 文件体积。

## Usage Examples
1. 基础调用（含Emoji）:
   `pdf-maker-pro(markdown_text="# 标题 🚀\\n- 状态: ✅ 已完成")`

2. 指定文件与图片（自适应+压缩）:
   `pdf-maker-pro(markdown_text="# 销售报表\\n![](C:/chart.png)", output_filename="sales_q1.pdf", fit_image=true, compression_quality=75)`

## Returns
{"status": "success", "file_path": "absolute/path/to/file.pdf"}
"""
import sys
import os
import json
import re
import datetime
import importlib
from pathlib import Path
import io
try:
    from PIL import Image as PILImage
except ImportError:
    PILImage = None

# Reconfigure stdout for UTF-8 support
sys.stdout.reconfigure(encoding='utf-8')

# --- Dependency Check ---
def build_install_command(packages):
    quoted_executable = f'"{sys.executable}"'
    package_args = ' '.join(packages)
    return f'{quoted_executable} -m pip install --index-url https://pypi.tuna.tsinghua.edu.cn/simple/ {package_args}'


def ensure_dependencies():
    required = [
        ('reportlab', 'reportlab'),
        ('emoji', 'emoji'),
        ('PIL', 'Pillow'),
    ]
    missing_packages = []

    for module_name, package_name in required:
        try:
            importlib.import_module(module_name)
        except ImportError:
            missing_packages.append(package_name)

    if missing_packages:
        install_command = build_install_command(missing_packages)
        missing_list = ', '.join(missing_packages)
        print(
            "Missing required Python packages for pdf-maker-pro: "
            f"{missing_list}.\n"
            "This skill no longer installs dependencies at runtime.\n"
            f"Current Python: {sys.executable}\n"
            "Please install the missing packages manually, then retry.\n"
            f"Recommended command: {install_command}",
            file=sys.stderr,
        )
        sys.exit(1)

ensure_dependencies()

from reportlab.lib import colors
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import getSampleStyleSheet, ParagraphStyle
from reportlab.lib.units import inch, cm
from reportlab.platypus import SimpleDocTemplate, Paragraph, Spacer, Image, PageBreak, Table, TableStyle, KeepTogether
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.lib.enums import TA_JUSTIFY, TA_CENTER, TA_LEFT

import emoji

# --- Font Registration ---
def register_chinese_font():
    """
    Attempts to register a Chinese font available on Windows.
    Prioritizes Microsoft YaHei (msyh.ttc/ttf) for better quality and symbol support.
    Fallbacks to SimHei (simhei.ttf) or SimSun (simsun.ttc).
    """
    fonts_to_try = [
        # Path, FontName, IsTTC, TTCIndex
        (os.path.join(os.environ.get('WINDIR', 'C:\\Windows'), 'Fonts', 'msyh.ttc'), "MicrosoftYaHei", True, 0),
        (os.path.join(os.environ.get('WINDIR', 'C:\\Windows'), 'Fonts', 'msyh.ttf'), "MicrosoftYaHei", False, 0),
        (os.path.join(os.environ.get('WINDIR', 'C:\\Windows'), 'Fonts', 'simhei.ttf'), "SimHei", False, 0),
        (os.path.join(os.environ.get('WINDIR', 'C:\\Windows'), 'Fonts', 'simsun.ttc'), "SimSun", True, 0),
    ]
    
    registered_font_name = None
    
    for path, font_name, is_ttc, ttc_index in fonts_to_try:
        if os.path.exists(path):
            try:
                if is_ttc:
                    # ReportLab supports TTC via subfontIndex
                    pdfmetrics.registerFont(TTFont(font_name, path, subfontIndex=ttc_index))
                else:
                    pdfmetrics.registerFont(TTFont(font_name, path))
                    
                print(f"Successfully registered font: {font_name} from {path}", file=sys.stderr)
                registered_font_name = font_name
                break
            except Exception as e:
                print(f"Warning: Failed to register font {path}: {e}", file=sys.stderr)
                continue
                
    if not registered_font_name:
        print("Warning: No Chinese font found. Text might be garbled.", file=sys.stderr)
        return "Helvetica" # Fallback
        
    return registered_font_name

CHINESE_FONT = register_chinese_font()

# --- Emoji Support (Fallback) ---
def register_emoji_font():
    """
    Attempts to register Segoe UI Emoji font for fallback rendering.
    """
    emoji_font_name = None
    try:
        font_path = os.path.join(os.environ.get('WINDIR', 'C:\\Windows'), 'Fonts', 'seguiemj.ttf')
        if os.path.exists(font_path):
            pdfmetrics.registerFont(TTFont('SegoeUIEmoji', font_path))
            emoji_font_name = 'SegoeUIEmoji'
            print(f"Successfully registered emoji font: {emoji_font_name}", file=sys.stderr)
    except Exception as e:
        print(f"Warning: Failed to register emoji font: {e}", file=sys.stderr)
    return emoji_font_name

EMOJI_FONT = register_emoji_font()

def clean_text(text):
    """
    Cleans text to ensure compatibility with ReportLab PDF generation.
    Attempts to use Segoe UI Emoji font for emoji characters to avoid squares.
    """
    if not text:
        return text
        
    def replace_with_font_tag(emoji_char, data_dict):
        if EMOJI_FONT:
            return f'<font face="{EMOJI_FONT}">{emoji_char}</font>'
        else:
            # Fallback if no emoji font: remove or demojize
            # To avoid squares, removing is safest if font is missing
            return '' 
            
    # Use emoji.replace_emoji to wrap emojis in font tags
    # Requires emoji >= 2.0.0
    try:
        text_with_font = emoji.replace_emoji(text, replace=replace_with_font_tag)
        return text_with_font
    except AttributeError:
        # Fallback for older emoji versions
        return emoji.demojize(text)
    except Exception as e:
        print(f"Warning: Emoji processing failed: {e}", file=sys.stderr)
        return text

# --- Layout & Decorators ---
def header_footer_layout(canvas, doc):
    """
    Draws header and footer on each page.
    """
    canvas.saveState()
    page_width, page_height = A4
    
    # Colors
    accent_color = colors.HexColor('#2980b9')
    text_color = colors.HexColor('#7f8c8d')
    
    # --- Header ---
    # Draw a colored strip at the top (optional, maybe too much ink)
    # canvas.setFillColor(accent_color)
    # canvas.rect(0, page_height - 20, page_width, 20, fill=1, stroke=0)
    
    # Header Line
    canvas.setStrokeColor(colors.lightgrey)
    canvas.setLineWidth(0.5)
    canvas.line(72, page_height - 50, page_width - 72, page_height - 50)
    
    # Header Text (Right: Date)
    canvas.setFont('Helvetica', 9)
    canvas.setFillColor(text_color)
    date_str = datetime.datetime.now().strftime("%Y-%m-%d")
    canvas.drawRightString(page_width - 72, page_height - 45, f"{date_str}")
    
    # Header Text (Left: Branding/Title)
    canvas.drawString(72, page_height - 45, "Business Document")
    
    # --- Footer ---
    # Footer Line
    canvas.line(72, 50, page_width - 72, 50)
    
    # Page Number
    page_num = canvas.getPageNumber()
    canvas.drawCentredString(page_width / 2, 35, f"- {page_num} -")
    
    canvas.restoreState()

# --- Table Styles ---
def get_table_styles():
    """返回默认的表格样式"""
    from reportlab.platypus import TableStyle
    return TableStyle([
        # Header row
        ('BACKGROUND', (0, 0), (-1, 0), colors.HexColor('#2980b9')),
        ('TEXTCOLOR', (0, 0), (-1, 0), colors.whitesmoke),
        ('ALIGN', (0, 0), (-1, -1), 'CENTER'),
        ('FONTNAME', (0, 0), (-1, 0), CHINESE_FONT),
        ('FONTSIZE', (0, 0), (-1, 0), 12),
        ('BOTTOMPADDING', (0, 0), (-1, 0), 12),
        # Data rows
        ('FONTNAME', (0, 1), (-1, -1), CHINESE_FONT),
        ('FONTSIZE', (0, 1), (-1, -1), 10),
        ('TOPPADDING', (0, 1), (-1, -1), 8),
        ('BOTTOMPADDING', (0, 1), (-1, -1), 8),
        # Grid
        ('GRID', (0, 0), (-1, -1), 0.5, colors.grey),
        # Alternating row colors
        ('BACKGROUND', (0, 1), (-1, -1), colors.HexColor('#f8f9fa')),
        ('BACKGROUND', (0, 2), (-1, -1), colors.white),
    ])

# --- Templates & Styles ---
def get_stylesheet():
    styles = getSampleStyleSheet()
    
    # Define Custom Styles with better visual hierarchy
    
    # Title: Centered, Dark, Spaced
    styles.add(ParagraphStyle(
        name='ChineseTitle',
        parent=styles['Heading1'],
        fontName=CHINESE_FONT,
        fontSize=26,
        leading=32,
        alignment=TA_CENTER,
        spaceAfter=30,
        textColor=colors.HexColor('#2c3e50')
    ))
    
    # Heading 1: Left, Colored, Underline-ish effect via border (if supported) or just spacing
    styles.add(ParagraphStyle(
        name='ChineseHeading1',
        parent=styles['Heading1'],
        fontName=CHINESE_FONT,
        fontSize=18,
        leading=24,
        spaceBefore=20,
        spaceAfter=12,
        textColor=colors.HexColor('#2980b9'),
        # Add a bottom border for visual separation
        borderWidth=0, # ReportLab basic styling doesn't always render borders well on all readers
        # We rely on color and spacing for "Clean" look
    ))
    
    # Heading 2: Darker, smaller
    styles.add(ParagraphStyle(
        name='ChineseHeading2',
        parent=styles['Heading2'],
        fontName=CHINESE_FONT,
        fontSize=15,
        leading=20,
        spaceBefore=15,
        spaceAfter=8,
        textColor=colors.HexColor('#34495e')
    ))
    
    # Body: Justified, readable line height
    styles.add(ParagraphStyle(
        name='ChineseBody',
        parent=styles['Normal'],
        fontName=CHINESE_FONT,
        fontSize=11,
        leading=18, # Increased leading for readability
        alignment=TA_JUSTIFY,
        spaceAfter=10,
        firstLineIndent=0 # Modern business style often prefers no indent but space between paragraphs
    ))
    
    # List: Indented
    styles.add(ParagraphStyle(
        name='ChineseList',
        parent=styles['Normal'],
        fontName=CHINESE_FONT,
        fontSize=11,
        leading=18,
        leftIndent=20,
        bulletIndent=10,
        spaceAfter=5,
        textColor=colors.HexColor('#2c3e50')
    ))
    
    # Quote/Block: Indented, Italic-ish (if font supports) or Grey
    styles.add(ParagraphStyle(
        name='ChineseQuote',
        parent=styles['Normal'],
        fontName=CHINESE_FONT,
        fontSize=10,
        leading=16,
        leftIndent=30,
        rightIndent=30,
        spaceBefore=10,
        spaceAfter=10,
        textColor=colors.HexColor('#7f8c8d'),
        backColor=colors.HexColor('#f8f9fa'),
        borderPadding=10,
        borderRadius=4
    ))
    
    # Caption: Centered, Small, Grey
    styles.add(ParagraphStyle(
        name='ChineseCaption',
        parent=styles['Normal'],
        fontName=CHINESE_FONT,
        fontSize=9,
        leading=12,
        alignment=TA_CENTER,
        spaceBefore=6,
        spaceAfter=12,
        textColor=colors.HexColor('#7f8c8d')
    ))
    
    return styles

def create_pdf(markdown_text, output_filename=None, template="business_clean", fit_image=True, max_image_width=450, max_image_height=550, compression_quality=85):
    # 1. Output Path Handling
    if not output_filename:
        timestamp = datetime.datetime.now().strftime("%Y%m%d_%H%M%S")
        output_filename = f"document_{timestamp}.pdf"
    
    if not output_filename.endswith('.pdf'):
        output_filename += '.pdf'
        
    # Unified Workspace Handling
    if not os.path.isabs(output_filename):
        env_wd = os.environ.get("AURAMATE_WD")
        if env_wd:
            output_filename = os.path.join(env_wd, output_filename)
            
    output_path = os.path.abspath(output_filename)
    # Ensure directory exists
    os.makedirs(os.path.dirname(output_path), exist_ok=True)
    
    # 2. Setup Document
    doc = SimpleDocTemplate(
        output_path,
        pagesize=A4,
        rightMargin=72,
        leftMargin=72,
        topMargin=72,
        bottomMargin=72
    )
    
    styles = get_stylesheet()
    story = []
    
    # 3. Parse Content
    lines = markdown_text.split('\n')
    i = 0
    
    while i < len(lines):
        line = lines[i].strip()
        
        # Skip empty lines (but add spacer)
        if not line:
            story.append(Spacer(1, 0.1*inch))
            i += 1
            continue
        
        # Table Detection (| col1 | col2 |)
        if line.startswith('|') and line.endswith('|'):
            table_data = []
            table_rows = []
            
            # Collect all table rows
            j = i
            while j < len(lines):
                row_line = lines[j].strip()
                if row_line.startswith('|') and row_line.endswith('|'):
                    # Check if this is a separator line (|---|---|)
                    if re.match(r'^\|[\s\-:|]+\|$', row_line):
                        j += 1
                        continue
                    # Parse cells
                    cells = [cell.strip() for cell in row_line[1:-1].split('|')]
                    table_rows.append(cells)
                    j += 1
                else:
                    break
            
            # Build table if we have rows
            if table_rows:
                # Convert cells to Paragraphs with proper styling
                table_data = []
                for row_idx, row in enumerate(table_rows):
                    para_row = []
                    for cell in row:
                        cell_text = clean_text(cell)
                        if row_idx == 0:
                            # Header row - use bold
                            para_row.append(Paragraph(f"<b>{cell_text}</b>", styles['ChineseBody']))
                        else:
                            para_row.append(Paragraph(cell_text, styles['ChineseBody']))
                    table_data.append(para_row)
                
                # Create table with auto column widths
                num_cols = len(table_data[0]) if table_data else 1
                col_widths = [(doc.width - (num_cols - 1) * 0.5) / num_cols] * num_cols
                
                table = Table(table_data, colWidths=col_widths)
                table.setStyle(get_table_styles())
                story.append(table)
                story.append(Spacer(1, 0.2*inch))
            
            i = j
            continue
        
        # Title (# )
        if line.startswith('# '):
            text = clean_text(line[2:].strip())
            story.append(Paragraph(text, styles['ChineseTitle']))
            story.append(Spacer(1, 0.2*inch))
            i += 1
            continue
            
        # Heading 1 (## ) -> Mapped to H1 style for logic, but visually H2 size
        elif line.startswith('## '):
            text = clean_text(line[3:].strip())
            story.append(Paragraph(text, styles['ChineseHeading1']))
            i += 1
            continue
            
        # Heading 2 (### )
        elif line.startswith('### '):
            text = clean_text(line[4:].strip())
            story.append(Paragraph(text, styles['ChineseHeading2']))
            i += 1
            continue
            
        # List Item (- or * )
        elif line.startswith('- ') or line.startswith('* '):
            text = clean_text(line[2:].strip())
            # Use bullet character
            story.append(Paragraph(f"• {text}", styles['ChineseList']))
            i += 1
            continue
            
        # Quote (> )
        elif line.startswith('> '):
            text = clean_text(line[2:].strip())
            story.append(Paragraph(text, styles['ChineseQuote']))
            i += 1
            continue
            
        # Image (![](path))
        elif line.startswith('!['):
            match = re.search(r'\!\[(.*?)\]\((.*?)\)', line)
            if match:
                alt_text = match.group(1).strip()
                img_path = match.group(2)
                # Handle relative paths or quotes
                img_path = img_path.strip('"\'')
                
                # Check for parameters in alt_text (e.g., "Caption|w=300,h=200")
                img_width_override = None
                img_height_override = None
                
                if '|' in alt_text:
                    parts = alt_text.split('|')
                    alt_text = parts[0].strip()
                    params_str = parts[1].strip()
                    
                    # Parse parameters
                    for param in params_str.split(','):
                        param = param.strip()
                        if '=' in param:
                            key, value = param.split('=', 1)
                            key = key.strip().lower()
                            value = value.strip()
                            try:
                                if key in ('w', 'width'):
                                    img_width_override = float(value)
                                elif key in ('h', 'height'):
                                    img_height_override = float(value)
                            except ValueError:
                                pass
                
                if os.path.exists(img_path):
                    try:
                        # Auto-scale image to fit page
                        # A4 = 595.27 x 841.89 pt
                        # Margins = 72 * 2 = 144 pt
                        
                        # Use global max dimensions or override if provided
                        available_width = img_width_override if img_width_override else max_image_width
                        available_height = img_height_override if img_height_override else max_image_height
                        
                        # Process image: resize and compress if needed
                        # We use PIL to open, then resize if large, then save to buffer if compression needed
                        # ReportLab Image accepts filename or file-like object
                        
                        # Note: We need to import PIL here or ensure it's available
                        # ReportLab depends on Pillow, so it should be fine
                        from PIL import Image as PILImage
                        import io
                        
                        pil_img = PILImage.open(img_path)
                        orig_width, orig_height = pil_img.size
                        
                        # Calculate display size first (logic same as before)
                        display_width = orig_width
                        display_height = orig_height
                        factor = 1.0
                        
                        # 1. Fit Width first
                        # Convert points back to pixels approx (1 pt = 1/72 inch, screen usually 96 dpi)
                        # Here we just work with abstract units for aspect ratio
                        
                        # Scale down logic for display in PDF (ReportLab handles scaling, but we calculate size)
                        # We use ReportLab's utils to get image size in points
                        from reportlab.lib.utils import ImageReader
                        img_reader = ImageReader(img_path)
                        img_size = img_reader.getSize()
                        # Ensure size values are floats (handle potential string returns)
                        img_width_pt = float(img_size[0]) if img_size[0] else 0
                        img_height_pt = float(img_size[1]) if img_size[1] else 0
                        
                        final_width_pt = img_width_pt
                        final_height_pt = img_height_pt
                        
                        # Ensure available_width and available_height are floats
                        available_width = float(available_width) if available_width else 450
                        available_height = float(available_height) if available_height else 550
                        
                        if img_width_pt > available_width:
                            factor = available_width / img_width_pt
                            final_width_pt = available_width
                            final_height_pt = img_height_pt * factor
                            
                        if fit_image and final_height_pt > available_height:
                            factor_h = available_height / final_height_pt
                            final_height_pt = available_height
                            final_width_pt = final_width_pt * factor_h

                        # Force exact dimensions if both width and height are provided in override
                        if img_width_override and img_height_override:
                            final_width_pt = img_width_override
                            final_height_pt = img_height_override
                            
                        # Optimization: If image is huge but displayed small, resize it
                        # Or if user requested compression
                        # We re-save the image to a memory buffer with compression
                        img_buffer = io.BytesIO()
                        
                        # Convert to RGB if RGBA (JPEG doesn't support alpha)
                        if pil_img.mode in ('RGBA', 'LA') and (compression_quality < 100 or output_filename.endswith('.pdf')):
                            background = PILImage.new(pil_img.mode[:-1], pil_img.size, (255, 255, 255))
                            background.paste(pil_img, pil_img.split()[-1])
                            pil_img = background
                            
                        pil_img.save(img_buffer, format='JPEG', quality=compression_quality)
                        img_buffer.seek(0)
                        
                        # Create ReportLab Image from buffer
                        img_obj = Image(img_buffer)
                        img_obj.drawWidth = final_width_pt
                        img_obj.drawHeight = final_height_pt
                        # IMPORTANT: hAlign is ignored when inside Table, cell alignment controls it
                        
                        # Use Table to force image and caption to stay together
                        # KeepTogether sometimes fails if content is slightly too large
                        table_data = [[img_obj]]
                        
                        if alt_text:
                            # Add caption row
                            caption_para = Paragraph(alt_text, styles['ChineseCaption'])
                            table_data.append([caption_para])
                            
                        # Create Table with single column equal to available width
                        # This ensures centering works relative to page width
                        img_table = Table(table_data, colWidths=[available_width])
                        img_table.setStyle(TableStyle([
                            ('ALIGN', (0,0), (-1,-1), 'CENTER'),
                            ('VALIGN', (0,0), (-1,-1), 'TOP'),
                            ('LEFTPADDING', (0,0), (-1,-1), 0),
                            ('RIGHTPADDING', (0,0), (-1,-1), 0),
                            ('TOPPADDING', (0,0), (0,0), 0),       # No top padding for image
                            ('BOTTOMPADDING', (0,0), (0,0), 5),    # Space below image
                            ('TOPPADDING', (0,1), (-1,-1), 0),     # No extra top padding for caption
                            ('BOTTOMPADDING', (0,1), (-1,-1), 0),  # No extra bottom padding for caption
                        ]))
                        
                        story.append(img_table)
                        story.append(Spacer(1, 0.2*inch))
                    except Exception as e:
                        print(f"Warning: Failed to load/process image {img_path}: {e}", file=sys.stderr)
                        story.append(Paragraph(f"[Image Load Failed: {os.path.basename(img_path)}]", styles['ChineseBody']))
                else:
                    print(f"Warning: Image path not found: {img_path}", file=sys.stderr)
                    story.append(Paragraph(f"[Image Not Found: {os.path.basename(img_path)}]", styles['ChineseBody']))

        # Normal Text
        else:
            story.append(Paragraph(clean_text(line), styles['ChineseBody']))
        
        i += 1
            
    # 4. Build PDF
    try:
        # Use header_footer_layout for both first and later pages
        doc.build(story, onFirstPage=header_footer_layout, onLaterPages=header_footer_layout)
        return output_path
    except Exception as e:
        raise Exception(f"PDF Build Failed: {e}")

def main():
    input_str = os.environ.get("SKILL_INPUT", "{}")
    try:
        params = json.loads(input_str)
        markdown_text = params.get("markdown_text")
        
        # Check if called with arguments (fallback)
        if not markdown_text and len(sys.argv) > 1:
            # Simple CLI usage: python pdf-maker-pro.py "markdown" "output.pdf"
            markdown_text = sys.argv[1]
            if len(sys.argv) > 2:
                params["output_filename"] = sys.argv[2]
        
        if not markdown_text:
            print(json.dumps({"status": "error", "message": "markdown_text is required"}))
            sys.exit(0)
            
        output_filename = params.get("output_filename")
        template = params.get("template", "business_clean")
        fit_image = params.get("fit_image", True)
        max_image_width = params.get("max_image_width", 450)
        max_image_height = params.get("max_image_height", 550)
        # Ensure compression_quality is an integer (fix "Invalid quality setting" error)
        compression_quality = int(params.get("compression_quality", 85))
        
        try:
            pdf_path = create_pdf(markdown_text, output_filename, template, fit_image, max_image_width, max_image_height, compression_quality)
            print(json.dumps({
                "status": "success", 
                "file_path": pdf_path
            }))
        except Exception as e:
            # Print full stack trace to stderr for debugging, JSON to stdout
            import traceback
            traceback.print_exc(file=sys.stderr)
            print(json.dumps({"status": "error", "message": str(e)}))
            
    except Exception as e:
        print(json.dumps({"status": "error", "message": f"System error: {str(e)}"}))

if __name__ == "__main__":
    main()
