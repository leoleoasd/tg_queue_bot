package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	tb "gopkg.in/tucnak/telebot.v2"
	"gopkg.in/yaml.v2"
)

type Status int

const (
	Doing   Status = 0
	Holding Status = 1
	Waiting Status = 2
)

type UserInQueue struct {
	User   *tb.User `json:"user"`
	Status Status   `json:"status"`
}

type Queue struct {
	Users   []UserInQueue `json:"users"`
	Message *tb.Message   `json:"message"`
	Max     int           `json:"max"`
	Args    []string      `json:"args"`
	Creator *tb.User      `json:"creator"`
}

var Queues []*Queue
var group_id int64
var MsgToQue map[int]int
var admins = []int{}

func main() {
	file, err := os.Open("config.yml")
	if err != nil {panic(err)}
	token := ""
	bytes, err := ioutil.ReadAll(file)
	if err != nil {panic(err)}
	err = yaml.Unmarshal(bytes, struct{
		Token *string `yaml:"token"`
		GroupId *int64 `yaml:"group_id"`
		Admins *[]int `yaml:"admins"`
	}{&token, &group_id, &admins})
	if err != nil {panic(err)}

	b, err := tb.NewBot(tb.Settings{
		Token:  token,
		Poller: &tb.LongPoller{Timeout: time.Second},
	})

	if err != nil {
		log.Fatal(err)
		return
	}

	b.Handle("/start", func(m *tb.Message) {
		b.Send(m.Chat, "排队机器人 \n/new <同时人数> <隐藏的详细信息, 如密码> <公开的详细信息, 如门票> 新建排队 \n/join 加入队列 \n/hold 暂停 \n/unhold 继续 \n/exit 退出队列\n/status 查看队列详细信息\n/kick <id> 踢掉第id个人")
	})

	b.Handle("/help", func(m *tb.Message) {
		b.Send(m.Chat, "排队机器人 \n/new <同时人数> <隐藏的详细信息, 如密码> <公开的详细信息, 如门票> 新建排队 \n/join 加入队列 \n/hold 暂停 \n/unhold 继续 \n/exit 退出队列\n/status 查看队列详细信息\n/kick <id> 踢掉第id个人")
	})

	b.Handle("/debug", func(m *tb.Message) {
		t, _ := json.Marshal(m)
		b.Send(m.Chat, string(t))
	})

	// 创建队列
	b.Handle("/new", func(m *tb.Message) {
		if m.Chat.ID <= 0 {
			b.Send(m.Chat, "请在私聊中发送.", &tb.SendOptions{ReplyTo: m})
			return
		}
		// 遍历所有队列, 看创建者是否已经有队列了
		index := -1
		for inde1, queue := range Queues {
			if queue.Creator.ID == m.Sender.ID{
				index = inde1
			}
		}
		if index != -1 {
			b.Send(m.Chat, "你已经创建了一个队列, 请先 /close 关闭队列!", &tb.SendOptions{ReplyTo: m})
			return
		}
		// 获取参数
		sli := strings.Split(m.Text, " ")
		if len(sli) <= 2 {
			// 参数错误, 缺少人数
			b.Send(m.Chat, "参数错误!", &tb.SendOptions{ReplyTo: m})
			return
		}
		max_count_s := sli[1] // 人数字符串
		max_count, err := strconv.ParseInt(max_count_s, 10, 32) // 转成int
		if err != nil || max_count <= 0 { // 人数有问题
			b.Send(m.Chat, "参数错误!", &tb.SendOptions{ReplyTo: m})
			return
		}
		var toJoin []string
		if len(sli) > 3 {
			toJoin = sli[3:]
		}
		// 第一个参数: 人数
		// 第二个参数: 密码
		// 之后的参数都是公开参数
		// 把第三个开始的参数以及后的参数 用空格连接起来, 拼回字符串

		chatm, err := b.Send(&tb.Chat{ID: group_id},
			fmt.Sprintf("%s 刚创建了一个队列!\n队列的详细信息是: %s\n回复这条消息, 发送 /join 即可加入队列!\n记得先 /start 我哦!",
				m.Sender.FirstName, strings.Join(toJoin, " ")),
			&tb.SendOptions{ParseMode: tb.ModeHTML},
		)
		if err != nil {
			b.Send(m.Chat, fmt.Sprint("未知错误!", err), &tb.SendOptions{ReplyTo: m})
			return
		}
		nq := Queue{
			Users:   nil,
			Message: chatm, // 机器人在群里发的消息, 用于从回复消息找到队列
			Max:     int(max_count),
			Args:    sli[2:],
			Creator: m.Sender,
		}
		// 放进队列数组里
		Queues = append(Queues, &nq)
		b.Send(m.Chat, "新建队列成功!", &tb.SendOptions{ReplyTo: m})
	})

	b.Handle("/join", func(m *tb.Message) {
		// 加入队列
		if m.Chat.ID != group_id {
			b.Send(m.Chat, "请在群聊中发送.", &tb.SendOptions{ReplyTo: m})
			return
		}
		if m.ReplyTo == nil {
			b.Send(m.Chat, "你需要回复 xx创建了队列 的消息.", &tb.SendOptions{ReplyTo: m})
			return
		}
		// 找到回复的消息
		idToFind := m.ReplyTo.ID
		if _, err := MsgToQue[idToFind]; err {
			idToFind = MsgToQue[idToFind]
			// 从映射表里找到最初的消息
			// 这个表是每一次 队列信息更新的时候, 都会往群里发一个新的消息
			// 为了实现从新的消息的id找到队列的过程
			// 建立了一个从新的消息的id到队列最开始的消息的映射
		}
		index := -1
		q := &Queue{}
		for ind, que := range Queues {
			if que.Message.ID == idToFind {
				index = ind
				break
			}
		}
		if index == -1 {
			b.Send(m.Chat, "你需要回复 xx创建了队列 的消息.\n找不到你回复的消息的队列.", &tb.SendOptions{ReplyTo: m})
			return
		}
		// 找到了队列
		q = Queues[index]
		// 先遍历所有队列, 防止一个用户同时加入多个队列
		for _, queue := range Queues {
			for _, u := range queue.Users {
				if u.User.ID == m.Sender.ID {
					b.Send(m.Chat, "退出队列后才能再次加入队列!", &tb.SendOptions{ReplyTo: m})
					return
				}
			}
		}

		// 加入队列
		q.Users = append(q.Users, UserInQueue{m.Sender, Waiting})
		b.Send(m.Chat, "加入队列成功!\n记得**私聊机器人** /start 我哦~", &tb.SendOptions{ReplyTo: m, ParseMode: tb.ModeMarkdown})
		fmt.Println(m.Sender.FirstName, "加入了队列", q.String())
		q.CheckStatus(b)
	})

	b.Handle("/exit", func(m *tb.Message) {
		index := -1
		index2 := -1
		q := &Queue{}
		// 遍历队列找用户
		for inde1, queue := range Queues {
			for inde2, u := range queue.Users {
				if u.User.ID == m.Sender.ID {
					index = inde1
					index2 = inde2
				}
			}
		}
		if index == -1 || index2 == -1 {
			b.Send(m.Chat,"你还没加入任何队列", &tb.SendOptions{ReplyTo: m})
			return
		}
		q = Queues[index]
		// 删除这个人
		q.Users = append(q.Users[:index2], q.Users[index2+1:]...)
		fmt.Println(m.Sender.FirstName, "退出了队列", q.String())
		_, err = b.Send(m.Chat,"成功退出了队列.", &tb.SendOptions{ReplyTo: m})
		if err != nil {
			fmt.Println(err)
		}
		q.CheckStatus(b)
		return
	})

	b.Handle("/hold", func(m *tb.Message) {
		index := -1
		index2 := -1
		q := &Queue{}
		// 遍历队列找用户
		for inde1, queue := range Queues {
			for inde2, u := range queue.Users {
				if u.User.ID == m.Sender.ID {
					index = inde1
					index2 = inde2
				}
			}
		}
		if index == -1 || index2 == -1 {
			b.Send(m.Chat,"你还没加入任何队列", &tb.SendOptions{ReplyTo: m})
			return
		}
		q = Queues[index]
		// 把他状态改成暂停
		if q.Users[index2].Status == Waiting {
			q.Users[index2].Status = Holding
			_, err = b.Send(m.Chat,"成功暂停了队列.", &tb.SendOptions{ReplyTo: m})
		}else{
			_, err = b.Send(m.Chat,"已经暂停或者已经开始了!", &tb.SendOptions{ReplyTo: m})
		}
		if err != nil {
			fmt.Println(err)
		}
		q.CheckStatus(b)
		return
	})

	b.Handle("/unhold", func(m *tb.Message) {
		index := -1
		index2 := -1
		q := &Queue{}
		for inde1, queue := range Queues {
			for inde2, u := range queue.Users {
				if u.User.ID == m.Sender.ID {
					index = inde1
					index2 = inde2
				}
			}
		}
		if index == -1 || index2 == -1 {
			b.Send(m.Chat,"你还没加入任何队列", &tb.SendOptions{ReplyTo: m})
			return
		}
		q = Queues[index]
		// 同hold
		if q.Users[index2].Status == Holding {
			q.Users[index2].Status = Waiting
			_, err = b.Send(m.Chat,"成功恢复了队列.", &tb.SendOptions{ReplyTo: m})
		}else{
			_, err = b.Send(m.Chat,"没有暂停或者已经开始了!", &tb.SendOptions{ReplyTo: m})
		}
		if err != nil {
			fmt.Println(err)
		}
		q.CheckStatus(b)
		return
	})

	b.Handle("/status", func(m *tb.Message) {
		if m.Chat.ID > 0 {
			// 如果是私聊发的
			index := -1
			index2 := -1
			q := &Queue{}
			// 找队列
			for inde1, queue := range Queues {
				for inde2, u := range queue.Users {
					if u.User.ID == m.Sender.ID {
						index = inde1
						index2 = inde2
					}
				}
			}
			if index == -1 || index2 == -1 {
				b.Send(m.Chat, "你还没加入任何队列", &tb.SendOptions{ReplyTo: m})
				return
			}
			q = Queues[index]
			msg := fmt.Sprintf("由%s创建的队列: \n", q.Creator.FirstName)
			for i, u := range q.Users {
				msg += fmt.Sprintf("%d %s: %s\n",i + 1, u.User.FirstName, []string{"进行中", "暂停中", "等待中"}[u.Status])
			}
			doing_count := 0
			for _, u := range q.Users {
				if u.Status == Doing {
					doing_count++
				}
			}
			msg += fmt.Sprintf("共有%d人, %d人进行中, 最大同时进行%d人.\n", len(q.Users), doing_count, q.Max)
			if q.Users[index2].Status == Doing {
				// 如果已经进行中, 就发密码
				msg += strings.Join(q.Args, " ")
			} else {
				// 如果没有, 就先不发密码
				msg += strings.Join(q.Args[1:], " ")
			}
			b.Send(m.Chat, msg, &tb.SendOptions{ParseMode: tb.ModeHTML})
		}
		return
	})

	b.Handle("/kick", func(m *tb.Message) {
		// t人
		args := strings.Split(m.Text, " ")
		if len(args) < 2 {
			b.Send(m.Chat, "用法: /kick <index> \n强行踢掉第index个人.", &tb.SendOptions{ReplyTo: m})
			return
		}
		// 找第二个参数
		toKick, err := strconv.ParseInt(args[1],10,64)
		if err != nil || toKick <= 0 {
			b.Send(m.Chat, "用法: /kick <index> \n强行踢掉第index个人.", &tb.SendOptions{ReplyTo: m})
			return
		}
		if m.ReplyTo == nil {
			b.Send(m.Chat, "你需要回复 有队列详细信息 的消息", &tb.SendOptions{ReplyTo: m})
			return
		}
		idToFind := m.ReplyTo.ID
		if _, err := MsgToQue[idToFind]; err {
			// 同理, 根据映射找队列
			idToFind = MsgToQue[idToFind]
		}
		index := -1
		q := &Queue{}
		for ind, que := range Queues {
			if que.Message.ID == idToFind {
				index = ind
				break
			}
		}
		if index == -1 {
			b.Send(m.Chat, "找不到你回复的队列", &tb.SendOptions{ReplyTo: m})
			return
		}
		// 找到后
		q=Queues[index]

		if m.Chat.ID > 0 {
			// 私聊发必须是队列创建者
			if m.Sender.ID != q.Creator.ID {
				b.Send(m.Chat, "你需要是队列的创建者", &tb.SendOptions{ReplyTo: m})
				return
			}
		} else {
			// 还得检测一下是否是这个群
			if m.Chat.ID == group_id {
				// 群里发的话检测一下是不是管理员
				chatMem, err := b.ChatMemberOf(m.Chat, m.Sender)
				if err != nil {
				}else{
					if chatMem.Role != tb.Administrator && chatMem.Role != tb.Creator && m.Sender.ID != q.Creator.ID {
						b.Send(m.Chat, "你需要是管理员或者是队列的创建者", &tb.SendOptions{ReplyTo: m})
						return
					}
				}
			}
		}

		if len(q.Users) < int(toKick) {
			b.Send(m.Chat, "找不到你想踢出的人.")
		}
		_, err = b.Send(m.Chat,q.Users[toKick-1].User.FirstName + "被踢出了队列.", &tb.SendOptions{ReplyTo: m})
		q.Users = append(q.Users[:toKick-1], q.Users[toKick:]...)
		if err != nil {
			fmt.Println(err)
		}
		q.CheckStatus(b)
	})

	b.Handle("/close", func(m *tb.Message) {
		if m.Chat.ID > 0 {
			// 得是私聊发
			index := -1
			// 找队列
			for inde1, queue := range Queues {
				if queue.Creator.ID == m.Sender.ID{
					index = inde1
				}
			}
			if index == -1 {
				b.Send(m.Chat, "你还没创建任何队列", &tb.SendOptions{ReplyTo: m})
				return
			}
			// 删队列
			Queues = append(Queues[:index], Queues[index+1:]...)
			b.Send(m.Chat, "队列删除成功!", &tb.SendOptions{ParseMode: tb.ModeHTML})
		}
	})

	b.Handle("/update", func(m *tb.Message) {
		if m.Chat.ID > 0 {
			index := -1
			for inde1, queue := range Queues {
				if queue.Creator.ID == m.Sender.ID{
					index = inde1
				}
			}
			if index == -1 {
				b.Send(m.Chat, "你还没创建任何队列", &tb.SendOptions{ReplyTo: m})
				return
			}
			// 找到参数, 得多于1个
			if len(strings.Split(m.Text," "))<= 1{
				b.Send(m.Chat, "参数错误!", &tb.SendOptions{ReplyTo: m})
				return
			}
			q := Queues[index]
			q.Args = strings.Split(m.Text," ")[1:]
			b.Send(m.Chat, "队列编辑成功!", &tb.SendOptions{ParseMode: tb.ModeHTML})
		}
	})

	go func() {
		// 如果收到退出信号, (kill, ctrl+c)
		// 关掉机器人, 保存数据.
		osSignals := make(chan os.Signal, 1)
		signal.Notify(osSignals, os.Interrupt, os.Kill, syscall.SIGTERM)
		<-osSignals
		b.Stop()
	}()
	// 先读数据
	file, err = os.OpenFile("data.json", os.O_APPEND|os.O_CREATE|os.O_RDONLY, 0777)
	byt, err := ioutil.ReadAll(file)
	if err == nil {
		json.Unmarshal(byt, &struct{
			Q *[]*Queue `json:"q"`
			M *map[int]int `json:"m"`
		}{&Queues, &MsgToQue})
		if MsgToQue == nil {
			MsgToQue = make(map[int]int)
		}
		fmt.Printf("%s\n",string(byt))
	} else {
		fmt.Print(err)
	}

	b.Start() // 这句会阻塞直到机器人退出
	// 到这里, 机器人就已经退出了

	// 写回数据
	file, err = os.OpenFile("data.json", os.O_APPEND|os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0777)
	if err != nil {
		_ = fmt.Errorf("Could not open file to save!\n")
		j, _ := json.Marshal(struct{
			Q *[]*Queue `json:"q"`
			M *map[int]int `json:"m"`
		}{&Queues, &MsgToQue})
		_ = fmt.Errorf("Queue: %s\n", j)
		return
	}
	// 搞个临时struct用于json编码
	j, _ := json.Marshal(struct{
		Q *[]*Queue `json:"q"`
		M *map[int]int `json:"m"`
	}{&Queues, &MsgToQue})
	_, _ = file.Write(j)
	_ = file.Close()
}

// 更新队列
func (q *Queue) CheckStatus(b *tb.Bot) {
	// 看有几个人进行中
	doing_count := 0
	for _, u := range q.Users {
		if u.Status == Doing {
			doing_count++
		}
	}
	if doing_count < q.Max {
		for k, u := range q.Users {
			if u.Status == Waiting {
				q.Users[k].Status = Doing
				doing_count++
				b.Send(&tb.Chat{ID: group_id}, fmt.Sprint(u.User.FirstName, "加入了队列!"))
				b.Send(q.Users[k].User, fmt.Sprint("加入队列成功! \n队列的详细信息:\n", strings.Join(q.Args, " ")))
			}

			if doing_count == q.Max {
				break
			}
		}
	}
	// 群里发队列详情
	msg := fmt.Sprintf("由 %s 创建的队列: \n", q.Creator.FirstName)
	for i, u := range q.Users {
		if i > 5 {break}
		msg += fmt.Sprintf("%d-%s: %s\n",i + 1, u.User.FirstName, []string{"进行中", "暂停中", "等待中"}[u.Status])
	}
	msg += fmt.Sprintf(".......\n共有%d人, %d人进行中, 最大同时进行%d人.\n", len(q.Users), doing_count, q.Max)
	msg += "队列的详细信息是:" + strings.Join(q.Args[1:], " ") + "\n"
	msg += "回复本消息, 发送 /join 即可加入队列."
	m, err := b.Send(&tb.Chat{ID: group_id}, msg, &tb.SendOptions{ParseMode: tb.ModeHTML})
	if err != nil {
		fmt.Println(err)
	} else {
		// 更新 发的消息到代表队列的消息的映射
		MsgToQue[m.ID] = q.Message.ID
		fmt.Println(MsgToQue)
	}
	// 更新一下储存的数据
	file, err := os.OpenFile("data.json", os.O_APPEND|os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0777)
	if err != nil {
		_ = fmt.Errorf("Could not open file to save!\n")
		j, _ := json.Marshal(struct{
			Q *[]*Queue `json:"q"`
			M *map[int]int `json:"m"`
		}{&Queues, &MsgToQue})
		_ = fmt.Errorf("Queue: %s\n", j)
		return
	}
	j, _ := json.Marshal(struct{
		Q *[]*Queue `json:"q"`
		M *map[int]int `json:"m"`
	}{&Queues, &MsgToQue})
	_, _ = file.Write(j)
	_ = file.Close()

}

func (q *Queue) String() string {
	j, _ := json.Marshal(q)
	return string(j)
}
