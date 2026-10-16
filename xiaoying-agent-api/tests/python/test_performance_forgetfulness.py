import requests
import json
import time
import os
import sys
import shutil
import statistics
from datetime import datetime

# Configuration
BASE_URL = "http://localhost:3000"
WORKSPACE_DIR = os.path.join(os.getcwd(), "workspace_perf_test")
REPORT_FILE = "performance_report.csv"

# Ensure workspace exists
if not os.path.exists(WORKSPACE_DIR):
    os.makedirs(WORKSPACE_DIR)

class PerformanceTest:
    def __init__(self):
        self.results = []
        self.session_id = None

    def _print_step(self, message):
        print(f"\n{'='*50}\n[STEP] {message}\n{'='*50}")

    def _log_metric(self, scenario, turn, ttft, duration, success, notes=""):
        self.results.append({
            "timestamp": datetime.now().isoformat(),
            "scenario": scenario,
            "turn": turn,
            "ttft_ms": round(ttft * 1000, 2),
            "duration_ms": round(duration * 1000, 2),
            "success": success,
            "notes": notes
        })

    def run_turn(self, scenario, turn_idx, prompt, validation_fn=None):
        print(f"\n--- Scenario: {scenario} | Turn: {turn_idx} ---")
        print(f"User: {prompt[:100]}..." if len(prompt) > 100 else f"User: {prompt}")

        url = f"{BASE_URL}/v1/chat/completions"
        headers = {"Content-Type": "application/json"}
        
        # Construct messages history - simpler for this test, we might just send the whole history
        # But wait, the standard /v1/chat/completions endpoint usually expects the full history 
        # OR if the backend maintains session state, we might just send the new message?
        # Looking at auramate backend, it seems to support stateful sessions if we provide a session ID?
        # Or standard OpenAI API which is stateless.
        # auramate's agent usually maintains state.
        # Let's check `test_sota_diversity_v4.py` again. It sends just the NEW message?
        # "messages": [{"role": "user", "content": prompt}]
        # If the backend is stateful (Agent), it will append. 
        # Auramate backend usually creates a session.
        # Let's assume stateful for now, but usually OpenAI API requires full history.
        # However, `agent.Run` in backend takes `sessionID`. 
        # The HTTP handler probably maps to a session.
        # Let's assume we need to send just the new message if we rely on backend session persistence,
        # OR send full history if it's stateless.
        # BUT `test_sota_diversity_v4.py` only sent one message at a time in its `run_test_scenario`.
        # Wait, `test_sota_diversity_v4.py` runs independent scenarios.
        # I need MULTI-TURN.
        # If I send just the new message, how does the backend know it's the SAME session?
        # The backend likely creates a new session for each request unless `session_id` is passed?
        # Let's check the backend HTTP handler if possible, or just try to pass a session ID if supported.
        # Actually, for standard OpenAI compatibility, the client holds the history.
        # BUT Auramate is an Agent, it has "Memory".
        # Let's try sending just the new message and see if it remembers (Stateful Backend).
        # If not, we might need to send full history. 
        # Given "Session SummaryMessageID" in previous turn, it implies backend state.
        # To be safe and test "Forgetfulness" of the AGENT (not just my client history),
        # I should probably rely on the Agent's internal memory/session if possible.
        # However, standard `test_sota_diversity_v4.py` didn't seem to use a specific session ID param in the body.
        # It might be using a new session every time?
        # Let's look at `server.go` or `handler.go` if we could, but I can't easily.
        # Assumption: The backend treats each /chat/completions call as a turn in a session IF a session_id is provided,
        # OR it might generate a new session each time.
        # IF it generates a new session each time, multi-turn won't work unless I send history.
        # Strategy: I will maintain `self.history` client-side and send it all. 
        # This is the standard OpenAI way. The Agent then processes it.
        # If the Agent has internal memory, sending full history helps it "load" the context.
        # Wait, if I send full history every time, I am testing the Context Window limit and the Agent's ability to handle it.
        # This effectively tests "Forgetfulness" via Context Limit -> Summary.
        
        if not hasattr(self, 'history'):
            self.history = []
        
        self.history.append({"role": "user", "content": prompt})
        
        data = {
            "model": "gpt-4o", # Or whatever default
            "messages": self.history,
            "stream": True,
            "temperature": 0
        }

        start_time = time.time()
        ttft = 0
        first_token_received = False
        full_content = ""
        
        try:
            response = requests.post(url, headers=headers, data=json.dumps(data), stream=True)
            response.raise_for_status()
            
            for line in response.iter_lines():
                if line:
                    decoded_line = line.decode('utf-8')
                    if decoded_line.startswith("data: "):
                        json_str = decoded_line[6:]
                        if json_str == "[DONE]":
                            break
                        try:
                            chunk = json.loads(json_str)
                            delta = chunk['choices'][0]['delta']
                            if 'content' in delta and delta['content']:
                                if not first_token_received:
                                    ttft = time.time() - start_time
                                    first_token_received = True
                                    print(f"[TTFT: {ttft*1000:.2f}ms] ", end="", flush=True)
                                
                                content_piece = delta['content']
                                full_content += content_piece
                                print(content_piece, end="", flush=True)
                        except:
                            pass
            
            duration = time.time() - start_time
            print(f"\n[Duration: {duration:.2f}s]")
            
            # Append assistant response to history
            self.history.append({"role": "assistant", "content": full_content})

            # Validation
            success = True
            notes = ""
            if validation_fn:
                try:
                    validation_fn(full_content)
                except Exception as e:
                    success = False
                    notes = str(e)
                    print(f"\n[Validation Failed] {e}")

            self._log_metric(scenario, turn_idx, ttft, duration, success, notes)
            return success

        except Exception as e:
            print(f"\n[Request Failed] {e}")
            self._log_metric(scenario, turn_idx, 0, 0, False, str(e))
            return False

    def save_report(self):
        print(f"\nSaving report to {REPORT_FILE}...")
        with open(REPORT_FILE, "w", encoding='utf-8') as f:
            f.write("timestamp,scenario,turn,ttft_ms,duration_ms,success,notes\n")
            for r in self.results:
                f.write(f"{r['timestamp']},{r['scenario']},{r['turn']},{r['ttft_ms']},{r['duration_ms']},{r['success']},{r['notes']}\n")
        print("Report saved.")
        
        # Print summary
        print("\n=== TEST SUMMARY ===")
        for r in self.results:
            status = "PASS" if r['success'] else "FAIL"
            print(f"[{status}] {r['scenario']} Turn {r['turn']} | TTFT: {r['ttft_ms']}ms | Dur: {r['duration_ms']}ms | {r['notes']}")


# --- Scenario A: PPT Generation ---
def test_ppt(tester):
    tester.history = [] # Reset history
    scenario_name = "PPT_Generation"
    
    # Turn 1
    prompt1 = "Create a 5-slide PPT about 'History of AI' with a title slide, 3 content slides (1950s, 1980s, 2020s), and a conclusion. Save as 'history_of_ai.pptx' in the workspace. Use 'python-pptx' library."
    def verify1(content):
        f = os.path.join(WORKSPACE_DIR, "history_of_ai.pptx")
        if not os.path.exists(f): raise Exception("PPT file not created")
    tester.run_turn(scenario_name, 1, prompt1, verify1)
    
    # Turn 2
    prompt2 = "Add a bullet point 'Deep Learning Boom' to the 2020s slide. Update the file."
    def verify2(content):
        # Difficult to verify content without reading PPT, check timestamp/size change
        f = os.path.join(WORKSPACE_DIR, "history_of_ai.pptx")
        if not os.path.exists(f): raise Exception("PPT file missing")
    tester.run_turn(scenario_name, 2, prompt2, verify2)
    
    # Turn 3
    prompt3 = "Save a copy as 'history_of_ai_v2.pptx' and change the title of the first slide to 'AI: Past, Present, Future'."
    def verify3(content):
        f = os.path.join(WORKSPACE_DIR, "history_of_ai_v2.pptx")
        if not os.path.exists(f): raise Exception("v2 PPT file not created")
    tester.run_turn(scenario_name, 3, prompt3, verify3)
    
    # Turn 4 (Memory Check)
    prompt4 = "What was the topic of the slide between 1950s and 2020s that we defined in the first step? Please just tell me the topic name."
    def verify4(content):
        if "1980" not in content and "1980s" not in content:
            raise Exception(f"Memory Fail: Expected '1980s', got '{content[:50]}...'")
    tester.run_turn(scenario_name, 4, prompt4, verify4)

# --- Scenario B: Excel Analysis ---
def test_excel(tester):
    tester.history = []
    scenario_name = "Excel_Analysis"
    
    # Turn 1
    prompt1 = "Create a mock CSV 'sales_data.csv' in workspace with columns: Date, Product (A/B/C), Amount (random 10-100), 50 rows."
    def verify1(content):
        f = os.path.join(WORKSPACE_DIR, "sales_data.csv")
        if not os.path.exists(f): raise Exception("CSV not created")
    tester.run_turn(scenario_name, 1, prompt1, verify1)
    
    # Turn 2
    prompt2 = "Read the CSV and calculate total sales per Product. Save the summary to 'sales_summary.xlsx'."
    def verify2(content):
        f = os.path.join(WORKSPACE_DIR, "sales_summary.xlsx")
        if not os.path.exists(f): raise Exception("Excel summary not created")
    tester.run_turn(scenario_name, 2, prompt2, verify2)
    
    # Turn 3 (Memory Check)
    prompt3 = "What were the columns in the original CSV I asked you to create? List them."
    def verify3(content):
        lower_c = content.lower()
        if not ("date" in lower_c and "product" in lower_c and "amount" in lower_c):
            raise Exception("Memory Fail: Columns mismatch")
    tester.run_turn(scenario_name, 3, prompt3, verify3)

# --- Scenario C: Image Processing ---
def test_image(tester):
    tester.history = []
    scenario_name = "Image_Processing"
    
    # Turn 1
    prompt1 = "Create a red square image (100x100) using Python (PIL), save it as 'red_square.png'."
    def verify1(content):
        f = os.path.join(WORKSPACE_DIR, "red_square.png")
        if not os.path.exists(f): raise Exception("Image not created")
    tester.run_turn(scenario_name, 1, prompt1, verify1)
    
    # Turn 2
    prompt2 = "Resize it to 200x200 and save as 'red_square_resized.png'."
    def verify2(content):
        f = os.path.join(WORKSPACE_DIR, "red_square_resized.png")
        if not os.path.exists(f): raise Exception("Resized image not created")
    tester.run_turn(scenario_name, 2, prompt2, verify2)
    
    # Turn 3 (Memory Check)
    prompt3 = "What was the size of the FIRST image we created?"
    def verify3(content):
        if "100" not in content:
            raise Exception(f"Memory Fail: Expected 100, got '{content}'")
    tester.run_turn(scenario_name, 3, prompt3, verify3)

# --- Scenario D: Travel Research ---
def test_travel(tester):
    tester.history = []
    scenario_name = "Travel_Research"
    
    # Turn 1
    prompt1 = "Research 'Top 3 ramen shops in Tokyo' using web search. Just list their names."
    def verify1(content):
        if len(content) < 10: raise Exception("Response too short")
    tester.run_turn(scenario_name, 1, prompt1, verify1)
    
    # Turn 2
    prompt2 = "Save this list to 'tokyo_ramen.md' in the workspace."
    def verify2(content):
        f = os.path.join(WORKSPACE_DIR, "tokyo_ramen.md")
        if not os.path.exists(f): raise Exception("Markdown file not created")
    tester.run_turn(scenario_name, 2, prompt2, verify2)
    
    # Turn 3 (Memory Check)
    prompt3 = "Which city are we looking for ramen in? Answer in one word."
    def verify3(content):
        if "Tokyo" not in content and "tokyo" not in content:
            raise Exception("Memory Fail: Wrong city")
    tester.run_turn(scenario_name, 3, prompt3, verify3)

def main():
    tester = PerformanceTest()
    
    try:
        test_ppt(tester)
    except Exception as e:
        print(f"Scenario PPT Failed: {e}")
        
    try:
        test_excel(tester)
    except Exception as e:
        print(f"Scenario Excel Failed: {e}")
        
    try:
        test_image(tester)
    except Exception as e:
        print(f"Scenario Image Failed: {e}")

    try:
        test_travel(tester)
    except Exception as e:
        print(f"Scenario Travel Failed: {e}")

    tester.save_report()

if __name__ == "__main__":
    main()
