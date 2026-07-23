package main

import (
	"regexp"
	"strings"
)

// reInterHanSpace matches a space between two Han characters — Classical Chinese
// is written without spaces, and dropping them also saves separator tokens.
//
//nolint:gochecknoglobals // compiled once
var reInterHanSpace = regexp.MustCompile(`(\p{Han})\s+(\p{Han})`)

// wenyanMap maps common English content words to a single Classical Chinese
// (文言) character. Hand-curated for correctness over coverage. Used by the
// wenyan / wenyan-all / ultra-wenyan modes, which swap surviving English words
// for their 文言 character after reduction. Dense per concept; a net token win
// on CJK-optimized tokenizers (Qwen, DeepSeek), roughly neutral to lossy on
// OpenAI's cl100k.
//
//nolint:gochecknoglobals // static lexicon
var wenyanMap = map[string]string{
	// pronouns / particles
	"i": "吾", "you": "爾", "he": "其", "she": "其", "we": "吾", "they": "其",
	"this": "此", "that": "彼", "here": "茲", "there": "彼",
	"not": "不", "no": "無", "yes": "諾", "all": "皆", "each": "每",
	"and": "及", "or": "或", "but": "然", "with": "以", "of": "之",
	"very": "甚", "more": "益", "most": "最", "again": "復", "also": "亦",

	// verbs
	"use": "用", "make": "作", "do": "為", "go": "往", "come": "來",
	"see": "見", "look": "視", "watch": "觀", "hear": "聞", "say": "曰",
	"speak": "言", "tell": "告", "ask": "問", "answer": "答", "call": "呼",
	"know": "知", "think": "思", "understand": "悟", "believe": "信",
	"want": "欲", "need": "需", "give": "予", "take": "取", "get": "得",
	"have": "有", "be": "是", "eat": "食", "drink": "飲", "run": "奔",
	"walk": "行", "fly": "飛", "sit": "坐", "stand": "立", "sleep": "寢",
	"live": "生", "die": "死", "love": "愛", "hate": "惡", "fear": "懼",
	"learn": "學", "study": "學", "teach": "教", "read": "讀", "write": "書", "build": "築",
	"create": "造", "destroy": "毀", "kill": "殺", "fight": "戰", "win": "勝",
	"lose": "敗", "buy": "購", "sell": "售", "pay": "付", "find": "尋",
	"search": "索", "help": "助", "work": "勞", "rest": "息", "open": "開",
	"close": "閉", "start": "始", "stop": "止", "end": "終", "change": "變",
	"move": "動", "keep": "守", "follow": "從", "lead": "領", "send": "送",
	"receive": "收", "show": "示", "hide": "隱", "break": "破", "fix": "修",
	"wait": "待", "choose": "擇", "add": "增", "remove": "除", "cut": "切",
	"join": "合", "split": "分", "push": "推", "pull": "引", "hold": "持",
	"carry": "負", "rise": "升", "fall": "落", "grow": "長", "forget": "忘",
	"remember": "憶", "return": "還", "wish": "願", "seem": "似", "become": "成",

	// nouns
	"person": "人", "people": "民", "man": "男", "woman": "女", "child": "兒",
	"king": "王", "god": "神", "water": "水", "fire": "火", "earth": "土",
	"wind": "風", "mountain": "山", "river": "河", "sea": "海", "sky": "天",
	"sun": "日", "moon": "月", "star": "星", "cloud": "雲", "rain": "雨",
	"snow": "雪", "tree": "樹", "flower": "花", "grass": "草", "wood": "木",
	"stone": "石", "gold": "金", "iron": "鐵", "jade": "玉", "horse": "馬",
	"dog": "犬", "cat": "貓", "bird": "鳥", "fish": "魚", "dragon": "龍",
	"tiger": "虎", "snake": "蛇", "cow": "牛", "sheep": "羊", "house": "屋",
	"gate": "門", "door": "戶", "wall": "牆", "road": "道", "city": "城",
	"country": "國", "land": "地", "field": "田", "mouth": "口", "eye": "目",
	"ear": "耳", "hand": "手", "foot": "足", "head": "首", "heart": "心",
	"body": "身", "blood": "血", "bone": "骨", "hair": "髮", "face": "顏",
	"name": "名", "word": "字", "book": "書", "paper": "紙", "letter": "信",
	"music": "樂", "art": "藝", "war": "戰", "peace": "和", "law": "法",
	"power": "力", "money": "財", "food": "食", "rice": "米", "tea": "茶",
	"wine": "酒", "medicine": "藥", "disease": "疾", "death": "死", "life": "生",
	"time": "時", "day": "日", "night": "夜", "year": "年", "month": "月",
	"morning": "晨", "way": "道", "thing": "物", "place": "處", "world": "世",
	"mind": "意", "soul": "魂", "spirit": "氣", "dream": "夢", "color": "色",
	"sound": "聲", "light": "光", "number": "數", "reason": "故", "matter": "事",
	"question": "問", "friend": "友", "enemy": "敵", "master": "主",

	// adjectives
	"big": "大", "small": "小", "long": "長", "short": "短", "high": "高",
	"low": "低", "new": "新", "old": "舊", "good": "善", "bad": "惡",
	"right": "正", "wrong": "誤", "true": "真", "false": "偽", "fast": "速",
	"slow": "緩", "hot": "熱", "cold": "寒", "warm": "溫", "strong": "強",
	"weak": "弱", "hard": "難", "easy": "易", "deep": "深", "heavy": "重",
	"full": "滿", "empty": "空", "clean": "潔", "rich": "富", "poor": "貧",
	"wise": "智", "beautiful": "美", "ugly": "醜", "near": "近", "far": "遠",
	"whole": "全", "half": "半", "same": "同", "different": "異", "many": "眾",
	"few": "寡", "first": "首", "last": "終", "white": "白", "black": "黑",
	"red": "紅", "green": "綠", "blue": "青", "yellow": "黃", "clear": "明",

	// numbers
	"one": "一", "two": "二", "three": "三", "four": "四", "five": "五",
	"six": "六", "seven": "七", "eight": "八", "nine": "九", "ten": "十",
	"hundred": "百", "thousand": "千", "million": "萬",

	// computing (approximate classical glosses)
	"code": "碼", "data": "據", "file": "檔", "error": "誤", "test": "試",
	"system": "系", "network": "網", "server": "伺", "user": "戶", "key": "鑰",
	"value": "值", "list": "列", "type": "型", "path": "徑", "link": "鏈",
	"page": "頁", "model": "型", "token": "符", "machine": "機", "memory": "憶",
	"tool": "具", "agent": "使", "task": "務", "mode": "式", "input": "入",
	"output": "出", "function": "函", "object": "物", "class": "類", "method": "術",

	// more verbs (dev + general)
	"set": "設", "allow": "許", "deny": "拒", "enable": "啟", "disable": "禁",
	"apply": "施", "delete": "刪", "insert": "插", "update": "更", "select": "選",
	"filter": "濾", "sort": "序", "count": "計", "print": "印", "log": "錄",
	"load": "載", "save": "存", "exec": "執", "parse": "析", "format": "格",
	"encode": "編", "decode": "解", "compress": "壓", "connect": "連", "request": "請",
	"respond": "應", "reply": "覆", "handle": "理", "process": "治", "manage": "管",
	"control": "控", "monitor": "督", "trace": "跡", "compile": "譯", "install": "裝",
	"configure": "置", "register": "註", "validate": "核", "verify": "驗", "check": "查",
	"examine": "察", "review": "閱", "approve": "准", "reject": "駁", "submit": "呈",
	"cancel": "撤", "resume": "續", "complete": "竟", "fail": "敗", "succeed": "濟",
	"raise": "舉", "replace": "換", "compare": "較", "match": "配", "reduce": "減",
	"group": "組", "define": "定", "assign": "賦", "deploy": "佈", "release": "釋",
	"introduce": "引", "exist": "存", "confirm": "證", "reflect": "映",
	"explain": "喻", "notice": "覺", "flag": "標", "merge": "併", "debug": "診",

	// tech nouns
	"host": "宿", "port": "埠", "header": "頭", "cache": "藏", "buffer": "緩",
	"queue": "隊", "stack": "疊", "thread": "緒", "disk": "碟", "storage": "儲",
	"database": "庫", "index": "引", "module": "模", "package": "包", "library": "冊",
	"framework": "架", "route": "線", "url": "址", "directory": "夾", "warning": "警",
	"exception": "異", "bug": "蟲", "issue": "題", "branch": "枝", "version": "版",
	"tag": "籤", "secret": "秘", "password": "碼", "credential": "憑", "session": "話",
	"permission": "權", "role": "職", "team": "隊", "project": "案", "job": "役",
	"service": "侍", "result": "果", "status": "態", "state": "況", "config": "置",
	"setting": "設", "option": "項", "param": "參", "variable": "變", "constant": "恆",
	"array": "陣", "string": "串", "integer": "整", "api": "介", "endpoint": "端",
	"behavior": "為", "author": "者", "security": "安", "credentials": "憑",
	"documentation": "文", "regression": "退", "functionality": "能", "vulnerability": "隙",

	// particles / common function words
	"if": "若", "when": "時", "where": "處", "because": "故", "while": "際",
	"until": "至", "after": "後", "before": "前", "now": "今", "today": "今",
	"always": "恆", "never": "莫", "often": "常", "must": "須", "should": "宜",
	"can": "能", "will": "將", "may": "或", "then": "則", "so": "乃",
	"both": "皆", "carefully": "慎", "clearly": "明", "immediately": "即", "whether": "否",
	"appropriate": "宜", "potential": "或", "existing": "存", "difficult": "難", "subtle": "微",
	"important": "要", "later": "後", "commit": "交", "message": "訊",
	"updated": "更", "changed": "變", "likely": "或", "really": "甚",
}

// applyWenyan replaces each English word with its 文言 character when the map
// has one; everything else (CJK, punctuation, code) passes through. No part-of-
// speech gate — the map is already content-word only.
func applyWenyan(text string) string {
	var b, word strings.Builder
	flush := func() {
		if word.Len() == 0 {
			return
		}
		w := word.String()
		if c, ok := wenyanMap[strings.ToLower(w)]; ok {
			b.WriteString(c)
		} else {
			b.WriteString(w)
		}
		word.Reset()
	}
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			word.WriteRune(r)
		} else {
			flush()
			b.WriteRune(r)
		}
	}
	flush()
	// Join adjacent 文言 characters (no spaces in Classical Chinese).
	out := b.String()
	for reInterHanSpace.MatchString(out) {
		out = reInterHanSpace.ReplaceAllString(out, "$1$2")
	}
	return out
}
