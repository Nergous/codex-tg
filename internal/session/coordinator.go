package session

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Nergous/codex-tg/internal/models"
	"github.com/Nergous/codex-tg/internal/state"
)

var ErrProjectNotAllowed = errors.New("project is not allow-listed")

type Codex interface { StartThread(context.Context,string)(string,error); ResumeThread(context.Context,string) error; StartTurn(context.Context,string,string)(string,error); InterruptTurn(context.Context,string,string) error }

type Coordinator struct { codex Codex; state *state.Store; projects map[string]models.Project; mu sync.Mutex; turns map[string]string }
func New(c Codex, s *state.Store, projects []models.Project) *Coordinator { p:=map[string]models.Project{}; for _, v:=range projects { p[v.Path]=v }; return &Coordinator{codex:c,state:s,projects:p,turns:map[string]string{}} }
func (c *Coordinator) OpenProject(ctx context.Context, path string, fresh bool) (models.Session,error) {
	if _,ok:=c.projects[path]; !ok { return models.Session{},ErrProjectNotAllowed }
	if !fresh { if s,err:=c.state.ActiveSession(ctx,path); err==nil { if err=c.codex.ResumeThread(ctx,s.ThreadID);err!=nil{return models.Session{},err}; return s,nil } else if !errors.Is(err,state.ErrNotFound){return models.Session{},err} }
	id,err:=c.codex.StartThread(ctx,path); if err!=nil{return models.Session{},err}; s:=models.Session{ProjectPath:path,ThreadID:id,Active:true}; if err=c.state.SetActiveSession(ctx,&s);err!=nil{return models.Session{},err}; return s,nil
}
func (c *Coordinator) Submit(ctx context.Context, thread,prompt string) error { c.mu.Lock(); defer c.mu.Unlock(); if c.turns[thread]!="" { return c.state.Enqueue(ctx,models.QueuedMessage{ThreadID:thread,Text:prompt}) }; id,err:=c.codex.StartTurn(ctx,thread,prompt); if err==nil { c.turns[thread]=id }; return err }
func (c *Coordinator) Complete(ctx context.Context, thread, turn string) error { c.mu.Lock(); defer c.mu.Unlock(); if c.turns[thread]==turn { delete(c.turns,thread) }; q,err:=c.state.Dequeue(ctx,thread); if errors.Is(err,state.ErrQueueEmpty){return nil}; if err!=nil{return err}; id,err:=c.codex.StartTurn(ctx,thread,q.Text); if err==nil { c.turns[thread]=id }; return err }
func (c *Coordinator) Cancel(ctx context.Context, thread string) error { c.mu.Lock(); defer c.mu.Unlock(); id:=c.turns[thread]; if id=="" {return nil}; if err:=c.codex.InterruptTurn(ctx,thread,id);err!=nil{return fmt.Errorf("interrupt turn: %w",err)}; return nil }
