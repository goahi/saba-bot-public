package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)
import (
	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

var s *discordgo.Session

type channelInfo struct {
	session *discordgo.Session
	channel string
}

func init() {
	err := godotenv.Load()
	if err != nil {
		log.Fatalf("Error loading .env file: %v", err)
	}

	token := os.Getenv("BOT_TOKEN")

	var errD error
	s, errD = discordgo.New("Bot " + token)
	if errD != nil {
		log.Fatalf("Invalid bot params: %v", errD)
	}
}

var (
	commands = []*discordgo.ApplicationCommand{
		{
			Name:        "memory",
			Description: "ごあい鯖のメモリ使用状況を表示します",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "表示するアプリケーションの名前",
					Required:    true,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{
							Name:  "letter-counter",
							Value: "letter-counter",
						},
					},
				},
			},
		},
		{
			Name:        "update",
			Description: "ごあい鯖のnodeアプリケーションを更新します",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "更新するアプリケーションの名前",
					Required:    true,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{
							Name:  "letter-counter",
							Value: "letter-counter",
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "branch",
					Description: "採用するGitHubブランチの名前(デフォルト: main)",
					Required:    false,
				},
			},
		},
		{
			Name:        "version",
			Description: "ごあい鯖のソフトウェアのバージョンを表示します",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "ソフトウェアの名前",
					Required:    true,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{
							Name:  "git",
							Value: "git",
						},
						{
							Name:  "go",
							Value: "go",
						},
						{
							Name:  "node",
							Value: "node",
						},
						{
							Name:  "npm",
							Value: "npm",
						},
						{
							Name:  "php",
							Value: "php",
						},
						{
							Name:  "pip",
							Value: "pip",
						},
						{
							Name:  "python",
							Value: "python",
						},
					},
				},
			},
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"update": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			appName := i.ApplicationCommandData().Options[0].StringValue()

			branch := "main"
			if len(i.ApplicationCommandData().Options) >= 2 {
				branch = i.ApplicationCommandData().Options[1].StringValue()
			}

			jst := time.FixedZone("Asia/Tokyo", 9*60*60)
			accept_time := time.Now().In(jst).Format("2006/01/02 15:04:05")

			var name string
			if i.Interaction.Member == nil {
				name = "名前が取得できませんでした"
			} else {
				name = i.Interaction.Member.Nick
				if name == "" {
					name = i.Interaction.Member.User.Username
				}
			}

			respBody := fmt.Sprintf("%vを更新します\n要求ユーザー: %v\n受理時刻: %v", appName, name, accept_time)
			resp := command_response(s, i, respBody)

			ch := i.ChannelID
			threadName := fmt.Sprintf("%v(%v)", appName, accept_time)
			rch, err := s.MessageThreadStart(ch, resp, threadName, 60)

			thread := channelInfo{s, rch.ID}

			if err != nil {
				log.Printf("Cannot start thread: %v", err)
			}

			switch appName {
			case "letter-counter":
				go thread.updateNodeApp(i.Interaction, "letter-counter", "/mnt/usb/share/letter-count/letter-counter", branch, "https://letter-counter.goahi.live")
			}

		},
		"memory": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			appName := i.ApplicationCommandData().Options[0].StringValue()

			switch appName {
			case "letter-counter":
				ratio, err := getMemoryRatio("node", "letter-counter")
				if err != nil {
					command_response(s, i, fmt.Sprint("エラーが発生しました: ", err))
					return
				}

				if ratio == 0 {
					command_response(s, i, "プロセスが見つかりませんでした")
					return
				}

				command_response(s, i, fmt.Sprintf("letter-counterのメモリ使用率: %.1f%%", ratio))
			}

		},
		"version": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			appName := i.ApplicationCommandData().Options[0].StringValue()

			switch appName {
			case "git":
				execRespond(s, i, "", "git", "--version")

			case "go":
				execRespond(s, i, "", "go", "version")

			case "node":
				execRespond(s, i, "node ", "node", "-v")

			case "npm":
				execRespond(s, i, "npm ", "npm", "-v")

			case "php":
				execRespond(s, i, "", "php", "-v")

			case "pip":
				command_response(s, i, "コマンドを実行しています……")
				replyChannel := channelInfo{s, i.ChannelID}
				replyChannel.sendExecResult("python3", "-m", "pip", "--version")

			case "python":
				execRespond(s, i, "", "python3", "--version")
			}
		},
	}
)

func (channel channelInfo) updateNodeApp(rt *discordgo.Interaction, appName string, path string, branch string, url string) {
	serviceName := appName + ".service"

	ese := &errSendExec{ch: channel}

	ese.err = os.Chdir(path)

	branchName := fmt.Sprintf("origin/%v", branch)
	ese.sendMessage("**GitHubから最新のコミットを取得しています……**")
	ese.sendExecResult("git", "fetch")
	ese.sendExecResult("git", "reset", "--hard", branchName)

	ese.sendMessage("**ビルドしています……**")
	ese.sendExecResult("npm", "install")
	ese.sendExecResult("npm", "run", "build")

	ese.sendMessage("**nodeサーバーを再起動しています……**")
	ese.sendExecResult("doas", "/usr/bin/systemctl", "restart", serviceName)

	rplyChannel := channelInfo{s, rt.ChannelID}
	r, _ := exec.Command("systemctl", "status", serviceName).CombinedOutput()
	if !strings.Contains(string(r), "Active: active (running)") {
		ese.err = fmt.Errorf("Serviceが正常に起動されていません")
	}

	if ese.err != nil {
		mes := fmt.Sprintf(":octagonal_sign: **%vの更新中にエラーが発生しました！**", appName)
		rplyChannel.sendMessage(rt.Member.User.Mention() + mes)
		channel.sendMessage(mes)
	} else {
		mes := fmt.Sprintf(" :tada: **%vの更新が完了しました！** \n デプロイ先: %v", appName, url)
		rplyChannel.sendMessage(rt.Member.User.Mention() + mes)
		channel.sendMessage(mes)
	}

	return
}

func getMemoryRatio(queries ...string) (ratio float64, err error) {
	grep := " | grep " + strings.Join(queries, " | grep ")

	r, err := exec.Command("bash", "-c", fmt.Sprintf("ps aux %v", grep)).CombinedOutput()
	if err != nil {
		log.Println(string(r))
		return 0, errors.New("メモリ使用量の取得に失敗しました")
	}

	rssColumn := 2
	re := regexp.MustCompile("[0-9.]+")
	usedR := re.FindAllString(string(r), -1)

	if len(usedR) < rssColumn {
		return 0, nil
	}

	used, err := strconv.ParseFloat(usedR[rssColumn], 64)
	if err != nil {
		log.Println(err)
		return 0, errors.New("メモリ使用量のパースに失敗しました")
	}

	return used, nil
}

type errSendExec struct {
	ch  channelInfo
	err error
}

func (e *errSendExec) sendExecResult(command string, arg ...string) {
	if e.err != nil {
		return
	}

	var r []byte
	r, e.err = exec.Command(command, arg...).CombinedOutput()
	e.ch.sendMessage(string(r))
}

func (chinfo channelInfo) sendExecResult(command string, arg ...string) {
	r, err := exec.Command(command, arg...).CombinedOutput()
	chinfo.sendMessage(string(r))
	if err != nil {
		chinfo.sendMessage(fmt.Sprint(err))
	}
}

func execRespond(se *discordgo.Session, it *discordgo.InteractionCreate, message string, command string, arg ...string) {
	r, err := exec.Command(command, arg...).CombinedOutput()
	command_response(se, it, message+string(r))
	if err != nil {
		command_response(se, it, fmt.Sprint(err))
	}

}

func (e errSendExec) sendMessage(message string) {
	if e.err == nil {
		e.ch.sendMessage(message)
	}
}

func (chinfo channelInfo) sendMessage(message string) {
	if strings.TrimSpace(message) == "" {
		//log.Println("Cannot send empty message")
		return
	}

	length := utf8.RuneCountInString(message)
	msgNum := length / 2000
	for i := 0; i < msgNum; i++ {
		_, err := chinfo.session.ChannelMessageSend(chinfo.channel, message[i*2000:(i+1)*2000+1])
		if err != nil {
			log.Printf("Cannnot send a message: %v", err)
		}
	}

	_, err := chinfo.session.ChannelMessageSend(chinfo.channel, message[msgNum*2000:])
	if err != nil {
		log.Printf("Cannnot send a message: %v", err)
	}
}

func command_response(se *discordgo.Session, it *discordgo.InteractionCreate, message string) string {
	err := se.InteractionRespond(it.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
		},
	})
	if err != nil {
		log.Printf("Cannot respond to command: %v", err)
	}

	mes, err := se.InteractionResponse(se.State.User.ID, it.Interaction)
	if err != nil {
		log.Printf("Cannot get response: %v", err)
	}
	return mes.ID
}

func init() {
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	})
}

func main() {
	s.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Logged in as: %v#%v", s.State.User.Username, s.State.User.Discriminator)
	})

	err := s.Open()
	if err != nil {
		log.Fatalf("Cannot open the session %v", err)
	}

	log.Println("adding commands")
	registeredCommands := make([]*discordgo.ApplicationCommand, len(commands))
	for i, v := range commands {
		cmd, err := s.ApplicationCommandCreate(s.State.User.ID, "737648130086010962", v)
		if err != nil {
			log.Panicf("Cannot create '%v' command: %v", v.Name, err)
		}
		_, err1 := s.ApplicationCommandCreate(s.State.User.ID, "742733779323453480", v)
		if err1 != nil {
			log.Panicf("Cannot create '%v' command: %v", v.Name, err)
		}
		registeredCommands[i] = cmd
	}

	defer s.Close()

	stop := make(chan os.Signal, 1)

	signal.Notify(stop, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGINT, os.Interrupt)
	log.Println("Press Ctrl+C to exit")
	<-stop

	commands, err := s.ApplicationCommands(s.State.User.ID, "")
	for i := range commands {
		command := commands[i]
		log.Printf("%v", command)
		err := s.ApplicationCommandDelete(s.State.User.ID, command.ID, "")
		if err != nil {
			log.Printf("Cannot delete command: %v", err)
		} else {
			log.Printf("%v was deleted!", command.Name)

		}
	}

	log.Println("exit")

}
