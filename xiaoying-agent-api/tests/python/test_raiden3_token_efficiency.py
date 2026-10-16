import requests
import json
import time
import os
import sys
import shutil

# Configuration
BASE_URL = "http://localhost:3000"
WORKSPACE_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../../workspace"))
GAME_FILE = os.path.join(WORKSPACE_DIR, "raiden3_game.py")

# Ensure UTF-8 output
sys.stdout.reconfigure(encoding='utf-8')

def print_step(message):
    print(f"\n{'='*50}\n[STEP] {message}\n{'='*50}")

def setup_game_environment():
    print_step("Setting up Game Environment")
    if not os.path.exists(WORKSPACE_DIR):
        os.makedirs(WORKSPACE_DIR)
    
    # Create a basic Pygame template (simulating existing code)
    basic_game_code = """import pygame
import random
import sys

# Initialize Pygame
pygame.init()

# Screen Dimensions
WIDTH, HEIGHT = 800, 600
screen = pygame.display.set_mode((WIDTH, HEIGHT))
pygame.display.set_caption("Raiden 3 Clone")

# Colors
WHITE = (255, 255, 255)
BLACK = (0, 0, 0)
RED = (255, 0, 0)

# Player
class Player(pygame.sprite.Sprite):
    def __init__(self):
        super().__init__()
        self.image = pygame.Surface((50, 40))
        self.image.fill(WHITE)
        self.rect = self.image.get_rect()
        self.rect.centerx = WIDTH // 2
        self.rect.bottom = HEIGHT - 10
        self.speed = 5

    def update(self):
        keys = pygame.key.get_pressed()
        if keys[pygame.K_LEFT]:
            self.rect.x -= self.speed
        if keys[pygame.K_RIGHT]:
            self.rect.x += self.speed
        if keys[pygame.K_UP]:
            self.rect.y -= self.speed
        if keys[pygame.K_DOWN]:
            self.rect.y += self.speed
            
        # Keep in bounds
        if self.rect.left < 0: self.rect.left = 0
        if self.rect.right > WIDTH: self.rect.right = WIDTH
        if self.rect.top < 0: self.rect.top = 0
        if self.rect.bottom > HEIGHT: self.rect.bottom = HEIGHT

    def shoot(self):
        bullet = Bullet(self.rect.centerx, self.rect.top)
        all_sprites.add(bullet)
        bullets.add(bullet)

# Bullet
class Bullet(pygame.sprite.Sprite):
    def __init__(self, x, y):
        super().__init__()
        self.image = pygame.Surface((5, 10))
        self.image.fill(RED)
        self.rect = self.image.get_rect()
        self.rect.centerx = x
        self.rect.bottom = y
        self.speed = -10

    def update(self):
        self.rect.y += self.speed
        if self.rect.bottom < 0:
            self.kill()

# Groups
all_sprites = pygame.sprite.Group()
bullets = pygame.sprite.Group()
player = Player()
all_sprites.add(player)

# Game Loop
running = True
clock = pygame.time.Clock()

while running:
    clock.tick(60)
    for event in pygame.event.get():
        if event.type == pygame.QUIT:
            running = False
        elif event.type == pygame.KEYDOWN:
            if event.key == pygame.K_SPACE:
                player.shoot()

    all_sprites.update()

    screen.fill(BLACK)
    all_sprites.draw(screen)
    pygame.display.flip()

pygame.quit()
"""
    with open(GAME_FILE, "w", encoding="utf-8") as f:
        f.write(basic_game_code)
    print(f"Created base game file at: {GAME_FILE}")

def run_task(task_description):
    url = f"{BASE_URL}/v1/chat/completions"
    headers = {"Content-Type": "application/json"}
    
    # We use a session ID to maintain context across multiple turns if needed,
    # but here we simulate a single long task execution.
    # To test the "Folding" logic, we need multi-turn interaction.
    # The backend Agent loop handles multi-turn internally for a single request 
    # if the agent decides to use tools multiple times.
    
    data = {
        "model": "qwen3.5-plus", # Use a capable model
        "messages": [
            {"role": "system", "content": "You are an expert Game Developer. Your goal is to improve the existing Pygame code."},
            {"role": "user", "content": task_description}
        ],
        "stream": True,
        "temperature": 0.1
    }

    print(f"🚀 Sending Task: {task_description[:100]}...")
    start_time = time.time()
    
    content = ""
    usage_stats = []
    
    try:
        response = requests.post(url, headers=headers, data=json.dumps(data), stream=True)
        response.raise_for_status()
        
        print("\n--- Agent Stream ---")
        for line in response.iter_lines():
            if line:
                decoded_line = line.decode('utf-8')
                if decoded_line.startswith("data: "):
                    json_str = decoded_line[6:]
                    if json_str == "[DONE]":
                        break
                    try:
                        chunk = json.loads(json_str)
                        
                        # Capture Usage at each step (if provided)
                        if 'usage' in chunk and chunk['usage']:
                            u = chunk['usage']
                            # Only record if it changes or is significant
                            if not usage_stats or u['prompt_tokens'] != usage_stats[-1]['prompt_tokens']:
                                usage_stats.append(u)
                                print(f"\n[METRIC] Input: {u.get('prompt_tokens')} | Output: {u.get('completion_tokens')} | Cache: {u.get('prompt_tokens_details', {}).get('cached_tokens', 0)}")

                        delta = chunk['choices'][0]['delta']
                        if 'content' in delta and delta['content']:
                            c = delta['content']
                            content += c
                            print(c, end="", flush=True)
                            
                        # Detect Tool Calls (optional visualization)
                        if 'tool_calls' in delta:
                            print(f"\n[TOOL CALL] {delta['tool_calls']}")
                            
                    except:
                        pass
        
        end_time = time.time()
        print(f"\n\n[INFO] Task Completed in {end_time - start_time:.2f}s")
        
        return usage_stats
        
    except Exception as e:
        print(f"\n[FAIL] Request Failed: {e}")
        return []

def main():
    setup_game_environment()
    
    # Task: Comprehensive improvement to trigger multiple tool calls (Read -> Think -> Edit -> Edit -> Read -> ...)
    # This ensures the history grows and triggers the folding logic.
    task = f"""
    我在 {GAME_FILE} 有一个基础的雷电3游戏代码。请帮我完善它，完成以下步骤：
    1. 读取当前代码理解结构。
    2. 添加敌人（Enemy）类：红色方块，从屏幕上方随机位置生成，向下移动。
    3. 添加碰撞检测：子弹击中敌人销毁两者；敌人撞到玩家游戏结束。
    4. 添加分数显示。
    5. 最后再次读取文件确认修改正确。
    请一步步思考并执行，确保代码可运行。
    """
    
    usage_history = run_task(task)
    
    print("\n" + "="*80)
    print("TOKEN CONSUMPTION ANALYSIS")
    print("="*80)
    print(f"{'Step':<5} | {'Input Tokens':<15} | {'Output Tokens':<15} | {'Cached Tokens':<15} | {'Delta Input'}")
    print("-" * 80)
    
    prev_input = 0
    for i, u in enumerate(usage_history):
        input_tok = u.get('prompt_tokens', 0)
        output_tok = u.get('completion_tokens', 0)
        cached_tok = u.get('prompt_tokens_details', {}).get('cached_tokens', 0)
        delta = input_tok - prev_input
        
        print(f"{i+1:<5} | {input_tok:<15} | {output_tok:<15} | {cached_tok:<15} | {delta:+d}")
        prev_input = input_tok

    print("-" * 80)
    
    # Verification of Logic
    print("\n[EVALUATION]")
    if len(usage_history) > 2:
        # Check if Input Tokens growth is flattened
        # Typically: Step 1 (Read) -> Large Input
        # Step 2 (Edit) -> Input should NOT grow by the full size of Step 1's output if folding works.
        
        # Let's look at the Delta.
        # If folding works, the delta between steps should be roughly (New Output + Overhead), 
        # NOT (Previous Total History).
        # Actually, since it's cumulative:
        # Without Folding: Input_N = Input_{N-1} + Output_{N-1}
        # With Folding:    Input_N = Input_{N-1} + Output_{N-1} - Folded_Content_Size
        
        # We can judge by the magnitude. 
        # If we read a file of 2000 chars (~500 tokens), and the next step input only grows by 50 tokens, folding worked.
        
        print("观察 Delta Input 列：")
        print("1. 如果 Delta 远小于上一步的 Output (特别是 Tool Result)，说明折叠生效。")
        print("2. 如果 Delta 随着轮次线性大幅增长，说明折叠未生效。")
        print("3. 特别注意 'read' 操作后的下一步，Input 是否暴涨。")
    else:
        print("Not enough steps to evaluate.")

if __name__ == "__main__":
    main()
