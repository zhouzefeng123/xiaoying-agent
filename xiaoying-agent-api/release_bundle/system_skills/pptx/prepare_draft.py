
import sys
from pptx import Presentation
from pptx.util import Inches, Pt
from pptx.enum.text import PP_ALIGN
from PIL import Image, ImageDraw

def create_image():
    # Create a simple blue image
    img = Image.new('RGB', (400, 300), color = (73, 109, 137))
    d = ImageDraw.Draw(img)
    d.text((10,10), "AI Future", fill=(255,255,0))
    img.save('ai_future.png')
    return 'ai_future.png'

def create_presentation():
    prs = Presentation()
    
    # Title Slide
    slide_layout = prs.slide_layouts[0] 
    slide = prs.slides.add_slide(slide_layout)
    title = slide.shapes.title
    subtitle = slide.placeholders[1]
    
    title.text = "AI Development Overview"
    subtitle.text = "Current State & Future Trends (2025)"
    
    # Content Slide 1
    bullet_slide_layout = prs.slide_layouts[1]
    slide = prs.slides.add_slide(bullet_slide_layout)
    shapes = slide.shapes
    
    title_shape = shapes.title
    body_shape = shapes.placeholders[1]
    
    title_shape.text = "Current State of AI"
    tf = body_shape.text_frame
    tf.text = "Large Language Models (LLMs) dominate"
    
    p = tf.add_paragraph()
    p.text = "Multimodal capabilities (Text, Image, Audio)"
    p.level = 1
    
    p = tf.add_paragraph()
    p.text = "Agentic workflows becoming standard"
    p.level = 1

    # Content Slide 2 with Image
    img_path = create_image()
    blank_slide_layout = prs.slide_layouts[6]
    slide = prs.slides.add_slide(blank_slide_layout)
    
    left = top = Inches(1)
    slide.shapes.add_picture(img_path, left, top, height=Inches(3.5))
    
    txBox = slide.shapes.add_textbox(Inches(1), Inches(5), Inches(5), Inches(1))
    tf = txBox.text_frame
    tf.text = "Visualizing the Future of AI"
    
    prs.save('draft_ai.pptx')
    print("Created draft_ai.pptx")

if __name__ == "__main__":
    try:
        create_presentation()
    except Exception as e:
        print(f"Error: {e}")
